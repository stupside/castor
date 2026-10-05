package mediaclient

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	"connectrpc.com/connect"

	mediav1 "github.com/stupside/castor/gen/castor/media/v1"
	castorv1 "github.com/stupside/castor/gen/castor/v1"
	"github.com/stupside/castor/services/apiserver/internal/device"
)

// Device is the connected device a drive lends; its lender connected it and closes it.
type Device interface {
	Play(ctx context.Context, streamURL *url.URL, container mediav1.Container) error
	AwaitEnd(ctx context.Context) error
	Capabilities() *mediav1.Capabilities
}

// Drive lends lent, shown as named, to cast id for the whole cast, playing what the media server asks until the cast ends or ctx does.
func (c *Client) Drive(parent context.Context, id string, lent Device, named *castorv1.Device) error {
	// Leaving ends the stream and every call on the device; the cast plays on without it.
	ctx, leave := context.WithCancelCause(parent)
	defer leave(nil)
	d := &driver{ctx: ctx, leave: leave, c: c, castID: id, device: lent, running: map[string]context.CancelFunc{}}
	defer d.release()
	stream, err := c.devices.Drive(ctx, &mediav1.DriveRequest{CastId: id, Device: named, Capabilities: lent.Capabilities()})
	if err != nil {
		return fmt.Errorf("driving cast: %w", err)
	}
	defer func() { _ = stream.Close() }()
	for stream.Receive() {
		if err := d.run(stream.Msg().GetCommand()); err != nil {
			return fmt.Errorf("driving cast: %w", err)
		}
	}
	if cause := context.Cause(ctx); cause != nil && parent.Err() == nil {
		return cause
	}
	if err := stream.Err(); err != nil && parent.Err() == nil {
		return fmt.Errorf("driving cast: %w", err)
	}
	return nil
}

// driver runs the media server's calls on the lent device, each concurrently, for one drive.
type driver struct {
	ctx    context.Context
	leave  context.CancelCauseFunc
	c      *Client
	castID string
	device Device

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
}

func (d *driver) run(cmd *mediav1.DeviceCommand) error {
	if cancel := cmd.GetCancel(); cancel != nil {
		d.mu.Lock()
		if stop, ok := d.running[cancel.GetCommandId()]; ok {
			stop()
		}
		d.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(d.ctx)
	d.mu.Lock()
	if _, exists := d.running[cmd.GetId()]; exists {
		d.mu.Unlock()
		cancel()
		return fmt.Errorf("media server reused running device command ID %q", cmd.GetId())
	}
	d.running[cmd.GetId()] = cancel
	d.mu.Unlock()
	d.wg.Go(func() {
		defer func() {
			d.mu.Lock()
			delete(d.running, cmd.GetId())
			d.mu.Unlock()
			cancel()
		}()
		answer := d.exec(ctx, cmd)
		// A cancelled call is abandoned on the server, which awaits no answer.
		if ctx.Err() != nil {
			return
		}
		answer.CastId, answer.CommandId = d.castID, cmd.GetId()
		d.answer(ctx, answer)
	})
	return nil
}

func (d *driver) exec(ctx context.Context, cmd *mediav1.DeviceCommand) *mediav1.AnswerRequest {
	switch c := cmd.GetCommand().(type) {
	case *mediav1.DeviceCommand_Play_:
		target, err := url.Parse(c.Play.GetUrl())
		if err != nil {
			return failure(err)
		}
		return outcome(d.device.Play(ctx, target, c.Play.GetContainer()))
	case *mediav1.DeviceCommand_AwaitEnd_:
		return outcome(d.device.AwaitEnd(ctx))
	}
	return failure(fmt.Errorf("device command %q is one this drive does not know", cmd.GetId()))
}

// release abandons every call still running: nothing drives the device past this drive.
func (d *driver) release() {
	d.mu.Lock()
	for _, stop := range d.running {
		stop()
	}
	d.mu.Unlock()
	d.wg.Wait()
}

func outcome(err error) *mediav1.AnswerRequest {
	if err != nil {
		return failure(err)
	}
	return &mediav1.AnswerRequest{Answer: &mediav1.AnswerRequest_Done_{Done: &mediav1.AnswerRequest_Done{}}}
}

func failure(err error) *mediav1.AnswerRequest {
	return &mediav1.AnswerRequest{Answer: &mediav1.AnswerRequest_Error{Error: deviceError(err)}}
}

// deviceError keeps a device's failure typed where the media server's recovery reads its kind.
func deviceError(err error) *mediav1.DeviceError {
	if gone, ok := errors.AsType[*device.Gone](err); ok {
		g := &mediav1.DeviceError_Gone{Device: gone.Device, Observed: gone.Observed}
		if gone.Err != nil {
			g.Cause = gone.Err.Error()
		}
		return &mediav1.DeviceError{Error: &mediav1.DeviceError_Gone_{Gone: g}}
	}
	// The contract refuses an empty message, which would end the drive rather than report the failure.
	return &mediav1.DeviceError{Error: &mediav1.DeviceError_Message{Message: cmp.Or(err.Error(), fmt.Sprintf("%T", err))}}
}

// answerTimeout bounds an answer, which runs past the call's own cancellation.
const answerTimeout = 5 * time.Second

// answer replies to the media server; an answer lost on the way would leave the cast waiting on it, so the drive ends instead.
func (d *driver) answer(ctx context.Context, req *mediav1.AnswerRequest) {
	answering, cancel := context.WithTimeout(context.WithoutCancel(ctx), answerTimeout)
	defer cancel()
	// NotFound is a call the media server already gave up on; it awaits nothing.
	if _, err := d.c.devices.Answer(answering, req); err != nil && connect.CodeOf(err) != connect.CodeNotFound {
		d.leave(fmt.Errorf("answering device command %s: %w", req.GetCommandId(), err))
	}
}
