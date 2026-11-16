package whisper

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/signal"

	wcpp "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
	"github.com/stupside/castor/services/mediaserver/internal/subtitle"
)

const (
	workerArgument   = "--castor-internal-whisper-worker-v1"
	workerFrameBytes = 8 << 20
	workerWindows    = 64
)

// The executable already contains the native library. Re-exec keeps its ABI and
// resources identical without shipping a second binary. This reserved argument
// is only an IPC worker: it does not initialize services or process CLI options.
func init() {
	if len(os.Args) == 2 && os.Args[1] == workerArgument {
		// The terminal's interrupt reaches this child with its parent, which ends it by closing its input.
		signal.Ignore(os.Interrupt)
		if err := serveWorker(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

type workerConfig struct {
	ModelPath string
	VADPath   string
	Language  string
}

type workerRequest struct {
	workerConfig
	Samples []float32
	Offset  float64
}
type workerResponse struct {
	Words []subtitle.Word
	Error string
}

func writeFrame(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(payload) > workerFrameBytes {
		return fmt.Errorf("native worker frame exceeds %d bytes", workerFrameBytes)
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(payload))); err != nil {
		return err
	}
	_, err = io.Copy(w, bytes.NewReader(payload))
	return err
}
func readFrame(r io.Reader, value any) error {
	var size uint32
	if err := binary.Read(r, binary.BigEndian, &size); err != nil {
		return err
	}
	if size == 0 || size > workerFrameBytes {
		return fmt.Errorf("invalid native worker frame size %d", size)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}
	return json.Unmarshal(payload, value)
}
func (r workerRequest) validate() error {
	if len(r.Samples) > 30*subtitle.SampleRate || len(r.ModelPath) > 8192 || len(r.VADPath) > 8192 || len(r.Language) > 32 || math.IsNaN(r.Offset) || math.IsInf(r.Offset, 0) {
		return errors.New("native recognition request exceeds limits")
	}
	return nil
}

// One process owns one model and invokes native code serially. Recycling bounds
// allocations leaked by upstream parameter setters; process exit reclaims them.
func serveWorker(in io.Reader, out io.Writer) error {
	var model wcpp.Model
	defer func() {
		if model != nil {
			_ = model.Close()
		}
	}()
	var config workerConfig
	for range workerWindows {
		var req workerRequest
		if err := readFrame(in, &req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if err := req.validate(); err != nil {
			return err
		}
		if model == nil {
			var err error
			model, err = wcpp.New(req.ModelPath)
			if err == nil {
				err = checkLanguage(model, req.Language)
			}
			if err != nil {
				return writeFrame(out, workerResponse{Error: err.Error()})
			}
			config = req.workerConfig
		}
		if req.workerConfig != config {
			return errors.New("native worker configuration changed")
		}
		words, err := (recognizer{model: model, language: req.Language, vadModelPath: req.VADPath}).Recognize(context.Background(), req.Samples, req.Offset)
		response := workerResponse{Words: words}
		if err != nil {
			response.Error = err.Error()
		}
		if err := writeFrame(out, response); err != nil {
			return err
		}
	}
	return nil
}
