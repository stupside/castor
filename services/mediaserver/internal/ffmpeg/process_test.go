package ffmpeg

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/stupside/castor/services/mediaserver/internal/media"
)

func TestWaitDoesNotOutrunTheStderrDrain(t *testing.T) {
	const last = "[error] castor-final-stderr-line"
	script := "sleep 0.2; i=0; while [ $i -lt 5000 ]; do echo \"[warning] line $i\" >&2; i=$((i+1)); done; echo " + last + " >&2"

	proc, err := Start(t.Context(), "/bin/sh", Command{Args: []string{"-c", script}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, proc.Stdout); err != nil {
		t.Fatal(err)
	}
	if err := proc.Wait(); err != nil {
		t.Fatal(err)
	}
	if lines := proc.Evidence().Lines; !slices.Contains(lines, last) {
		t.Errorf("the last line printed before exit is not in the %d retained", len(lines))
	}
}

func TestAnExitStatusSaysWhoEndedTheProcess(t *testing.T) {
	cases := []struct {
		name   string
		script string
		stop   func(*Process, context.CancelFunc)
		want   int
	}{
		{name: "an exit keeps its code", script: "exit 3", want: 3},
		{name: "a signal of its own is an exit that failed", script: "kill -SEGV $$", want: signalExitBase + int(syscall.SIGSEGV)},
		{name: "castor's Kill leaves no status", script: "sleep 30", stop: func(p *Process, _ context.CancelFunc) { p.Kill() }, want: noExitStatus},
		{name: "castor's cancellation leaves no status", script: "sleep 30", stop: func(_ *Process, cancel context.CancelFunc) { cancel() }, want: noExitStatus},
		{
			// Teardown always kills before it waits, so a crash must survive the kill that follows it.
			name:   "a crash that castor's Kill arrives after keeps its own signal",
			script: "kill -ABRT $$",
			stop: func(p *Process, _ context.CancelFunc) {
				<-p.scanned
				p.Kill()
			},
			want: signalExitBase + int(syscall.SIGABRT),
		},
		{
			// An exited, unreaped process still accepts a kill, so a crash by SIGKILL (the OOM killer's) must not read as castor's.
			name:   "a SIGKILL of its own that castor's Kill arrives after keeps its signal",
			script: "kill -KILL $$",
			stop: func(p *Process, _ context.CancelFunc) {
				<-p.scanned
				p.Kill()
			},
			want: signalExitBase + int(syscall.SIGKILL),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			proc, err := Start(ctx, "/bin/sh", Command{Args: []string{"-c", tc.script}}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if tc.stop != nil {
				tc.stop(proc, cancel)
			}
			_ = proc.Wait()
			if got := proc.Evidence().ExitStatus; got != tc.want {
				t.Errorf("ExitStatus = %d, want %d", got, tc.want)
			}
		})
	}
}

// An ffmpeg that dies before opening its side outputs must not leave their readers waiting for a connection.
func TestSideOutputsEndWhenFFmpegNeverOpensThem(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	cmd := NewCommand([]string{"-hide_banner", "-i", "/nonexistent/castor-input",
		"-progress", ProgressPipe, "-f", "s16le", PCMPipe})
	var pcm bytes.Buffer
	proc, err := Start(t.Context(), ffmpegPath, cmd, Options{PCM: &pcm, Progress: func(media.Progress) {}})
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() {
		_, _ = io.Copy(io.Discard, proc.Stdout)
		waited <- proc.Wait()
	}()
	select {
	case err := <-waited:
		if err == nil {
			t.Fatal("ffmpeg succeeded on a missing input")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Wait never returned: a side output's reader is still waiting for a connection that will not come")
	}
	if pcm.Len() != 0 {
		t.Errorf("got %d PCM bytes from an output ffmpeg never opened", pcm.Len())
	}
}

func TestProcessCancellationIsOneOperation(t *testing.T) {
	proc, err := Start(t.Context(), "/bin/sh", Command{Args: []string{"-c", "exec sleep 30"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.cmd.Cancel(); err != nil {
		t.Fatal(err)
	}
	_ = proc.Wait()
	// os/exec cancellation and explicit teardown share the original signal;
	// a later kill must not replace its outcome with ProcessDone.
	if err := proc.cmd.Cancel(); err != nil {
		t.Fatalf("repeated cancellation replaced original result: %v", err)
	}
	if got := proc.Evidence().ExitStatus; got != noExitStatus {
		t.Fatalf("own cancellation classified as exit %d", got)
	}
}

// ffmpeg's own exit codes reach past 128, so only the wait status says a signal ended it.
func TestASignalOfItsOwnIsToldApartFromAnExitSharingItsCode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		script   string
		kill     bool
		signaled bool
	}{
		{"an exit with a signal's code", "exit 139", false, false},
		{"a signal of its own", "kill -SEGV $$", false, true},
		{"castor's own kill", "sleep 30", true, false},
	} {
		proc, err := Start(t.Context(), "/bin/sh", Command{Args: []string{"-c", tc.script}}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if tc.kill {
			proc.Kill()
		}
		_ = proc.Wait()
		if got := proc.Evidence().Signaled; got != tc.signaled {
			t.Errorf("%s: signaled %t, want %t", tc.name, got, tc.signaled)
		}
	}
}
