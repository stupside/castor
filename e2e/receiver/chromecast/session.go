package chromecast

import (
	"encoding/binary"
	"encoding/json/v2"
	"io"
	"net"
	"sync"

	pb "github.com/vishen/go-chromecast/cast/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"

	"github.com/stupside/castor/e2e/receiver"
)

// castSession is one sender's connection.
type castSession struct {
	session  *receiver.Session
	conn     net.Conn
	cors     CORS
	wmu      sync.Mutex
	launched bool
	// content is what the last LOAD played, which PAUSE and PLAY report on.
	content string
}

// request is the part of any sender payload the receiver dispatches on.
type request struct {
	Type      string `json:"type"`
	RequestID int    `json:"requestId"`
	Media     struct {
		ContentID   string `json:"contentId"`
		ContentType string `json:"contentType"`
		Tracks      []struct {
			ContentID string `json:"trackContentId"`
		} `json:"tracks"`
	} `json:"media"`
	MediaSessionID int `json:"mediaSessionId"`
}

// handlers answer each namespace#type a sender sends; the reply goes back under the request's id.
func (c *castSession) handlers() map[string]func(msg *pb.CastMessage, req request) map[string]any {
	return map[string]func(*pb.CastMessage, request) map[string]any{
		"urn:x-cast:com.google.cast.tp.heartbeat#PING": func(*pb.CastMessage, request) map[string]any {
			return map[string]any{"type": "PONG"}
		},
		nsReceiver + "#LAUNCH": func(*pb.CastMessage, request) map[string]any {
			c.launched = true
			return c.receiverStatus()
		},
		nsReceiver + "#GET_STATUS": func(*pb.CastMessage, request) map[string]any { return c.receiverStatus() },
		nsReceiver + "#STOP": func(*pb.CastMessage, request) map[string]any {
			c.session.Stop()
			c.launched = false
			return c.receiverStatus()
		},
		nsMedia + "#STOP": func(*pb.CastMessage, request) map[string]any {
			c.session.Stop()
			return c.mediaStatus(c.content, "IDLE", "CANCELLED")
		},
		nsMedia + "#GET_STATUS": func(*pb.CastMessage, request) map[string]any {
			if c.content == "" {
				return map[string]any{"type": "MEDIA_STATUS", "status": []any{}}
			}
			return c.status()
		},
		nsMedia + "#LOAD": func(msg *pb.CastMessage, req request) map[string]any {
			content, sender := req.Media.ContentID, msg.GetSourceId()
			// The receiver plays one hand-off, so it refuses another as it refuses a stream it cannot fetch.
			if c.cors.refuses(c.session.Context(), content, req.Media.ContentType) || !c.session.Hand(content, req.Media.ContentType) {
				c.send(mediaTransportID, sender, nsMedia, c.mediaStatus(content, "IDLE", "ERROR"))
				return map[string]any{"type": "LOAD_FAILED"}
			}
			c.content = content
			for _, track := range req.Media.Tracks {
				c.fetchCaption(track.ContentID)
			}
			c.session.Go(func() { c.report(sender, content) })
			return c.mediaStatus(content, "BUFFERING", "")
		},
		nsMedia + "#PAUSE": func(_ *pb.CastMessage, req request) map[string]any { return c.transport(req, true) },
		nsMedia + "#PLAY":  func(_ *pb.CastMessage, req request) map[string]any { return c.transport(req, false) },
	}
}

// transport pauses or resumes the media session, answering with the state it is now in.
func (c *castSession) transport(req request, hold bool) map[string]any {
	if req.MediaSessionID != mediaSessionID {
		c.session.Problem("%s on media session %d, want %d", req.Type, req.MediaSessionID, mediaSessionID)
	}
	if c.session.State() != receiver.Playing {
		c.session.Problem("%s while not playing", req.Type)
		return c.mediaStatus(c.content, "IDLE", idleReasons[c.session.State()])
	}
	c.session.Hold(hold)
	return c.status()
}

func (c *castSession) status() map[string]any {
	switch state := c.session.State(); {
	case state.Over():
		return c.mediaStatus(c.content, "IDLE", idleReasons[state])
	case c.session.Held():
		return c.mediaStatus(c.content, "PAUSED", "")
	}
	return c.mediaStatus(c.content, "PLAYING", "")
}

func (c *castSession) serve() {
	handlers := c.handlers()
	for {
		var length uint32
		if err := binary.Read(c.conn, binary.BigEndian, &length); err != nil {
			return
		}
		frame := make([]byte, length)
		if _, err := io.ReadFull(c.conn, frame); err != nil {
			return
		}
		var msg pb.CastMessage
		if err := proto.Unmarshal(frame, protoadapt.MessageV2Of(&msg)); err != nil {
			c.session.Problem("undecodable cast frame: %v", err)
			return
		}
		var req request
		if err := json.Unmarshal([]byte(msg.GetPayloadUtf8()), &req); err != nil {
			c.session.Problem("undecodable cast payload on %s: %v", msg.GetNamespace(), err)
			continue
		}
		// Connection-namespace CONNECT/CLOSE need no answer.
		handle, ok := handlers[msg.GetNamespace()+"#"+req.Type]
		if !ok {
			continue
		}
		reply := handle(&msg, req)
		reply["requestId"] = req.RequestID
		c.send(msg.GetDestinationId(), msg.GetSourceId(), msg.GetNamespace(), reply)
	}
}

// report pushes PLAYING, then IDLE with the reason playback ended, the way a receiver tells a sender it is over.
func (c *castSession) report(sender, content string) {
	c.send(mediaTransportID, sender, nsMedia, c.mediaStatus(content, "PLAYING", ""))
	select {
	case <-c.session.Done():
	case <-c.session.Context().Done():
		return
	}
	c.send(mediaTransportID, sender, nsMedia, c.mediaStatus(content, "IDLE", idleReasons[c.session.State()]))
}

func (c *castSession) receiverStatus() map[string]any {
	apps := []any{}
	if c.launched {
		apps = append(apps, map[string]any{
			"appId": defaultMediaApp, "displayName": "Default Media Receiver", "isIdleScreen": false,
			"sessionId": "session-1", "statusText": "", "transportId": mediaTransportID,
		})
	}
	return map[string]any{"type": "RECEIVER_STATUS", "status": map[string]any{
		"applications": apps, "volume": map[string]any{"level": 1, "muted": false},
	}}
}

// mediaStatus carries currentTime as a receiver does: the media played so far.
func (c *castSession) mediaStatus(content, state, idleReason string) map[string]any {
	status := map[string]any{
		"mediaSessionId": mediaSessionID, "playerState": state, "idleReason": idleReason,
		"currentTime": c.session.Played().Seconds(), "media": map[string]any{"contentId": content},
	}
	return map[string]any{"type": "MEDIA_STATUS", "status": []any{status}}
}

// send frames one message; a sender may disconnect during teardown.
func (c *castSession) send(source, destination, namespace string, payload map[string]any) {
	body, err := json.Marshal(payload)
	if err != nil {
		c.session.Problem("encoding a cast payload: %v", err)
		return
	}
	text := string(body)
	frame, err := proto.Marshal(protoadapt.MessageV2Of(&pb.CastMessage{
		ProtocolVersion: pb.CastMessage_CASTV2_1_0.Enum(),
		SourceId:        &source,
		DestinationId:   &destination,
		Namespace:       &namespace,
		PayloadType:     pb.CastMessage_STRING.Enum(),
		PayloadUtf8:     &text,
	}))
	if err != nil {
		c.session.Problem("encoding a cast frame: %v", err)
		return
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := binary.Write(c.conn, binary.BigEndian, uint32(len(frame))); err != nil {
		return
	}
	_, _ = c.conn.Write(frame)
}
