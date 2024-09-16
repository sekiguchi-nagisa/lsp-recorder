package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type StreamType int

const (
	STDIN StreamType = iota
	STDOUT
	STDERR
)

func toString(t StreamType) string {
	switch t {
	case STDIN:
		return "<stdin>"
	case STDOUT:
		return "<stdout>"
	case STDERR:
		return "<stderr>"
	default:
		return ""
	}
}

type PayloadType int

const (
	INVALID PayloadType = iota // for invalid LSP message
	JSON
	RAW
)

type LogData struct {
	timestamp   time.Time
	streamType  StreamType
	payloadType PayloadType
	payload     []byte
}

func record(ctx context.Context, ch <-chan LogData, writer io.Writer) {
	for {
		select {
		case <-ctx.Done():
			return
		case v := <-ch:
			_, _ = fmt.Fprintf(writer, "%s %s", v.timestamp.Format(time.RFC3339Nano), toString(v.streamType))
			if v.payloadType != JSON {
				_, _ = writer.Write([]byte(" "))
				_, _ = writer.Write(v.payload)
				_, _ = writer.Write([]byte("\n"))
			} else {
				buf := bytes.Buffer{}
				buf.Grow(len(v.payload) * 2)
				if json.Indent(&buf, v.payload, "", "  ") != nil {
					_, _ = fmt.Fprintf(writer, "invalid json payload\n")
					_, _ = writer.Write(v.payload)
					_, _ = writer.Write([]byte("\n"))
				} else {
					_, _ = writer.Write([]byte("\n"))
					_, _ = writer.Write(buf.Bytes())
					_, _ = writer.Write([]byte("\n"))
				}
			}
		}
	}
}

func sendMessage(t StreamType, value string, ch chan<- LogData) {
	ch <- LogData{
		timestamp:   time.Now(),
		streamType:  t,
		payloadType: RAW,
		payload:     []byte(value),
	}
}

func logError(value string, ch chan<- LogData) {
	sendMessage(STDERR, value, ch)
	_, _ = os.Stderr.WriteString(value)
}

func parseContentHeader(buffer *bytes.Buffer) (int, error) {
	s := strings.Builder{}
	s.Grow(32)
	for _, b := range []byte("Content-Length: ") {
		r, e := buffer.ReadByte()
		s.WriteByte(r)
		if r != b || e != nil {
			return -1, fmt.Errorf("invalid message header: '%s'", buffer.String())
		}
	}

	s.Reset()
	for {
		r, e := buffer.ReadByte()
		if e != nil {
			return -1, errors.New("content length must be end with \\r\\n\\r\\n")
		}
		if r == '\r' {
			break
		}
		s.WriteByte(r)
	}
	for _, b := range []byte("\n\r\n") {
		if r, e := buffer.ReadByte(); e != nil || r != b {
			return -1, errors.New("content length must be end with \\r\\n\\r\\n")
		}
	}
	n, e := strconv.Atoi(s.String())
	if e != nil {
		return -1, e
	}
	if n <= 0 {
		return -1, errors.New("content length must be greater than 0")
	}
	return n, nil
}

func intercept(ctx context.Context, t StreamType, reader io.Reader, writer io.Writer, ch chan<- LogData) {
	buf := bytes.Buffer{}
	buf.Grow(2048)
	requiredPayloadLen := -1
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		tmp := make([]byte, 1024)
		n, _ := reader.Read(tmp) //FIXME: read error handling
		if n == 0 {
			continue // skip empty data
		}
		n, _ = writer.Write(tmp[:n]) //FIXME: write error handling

		if t == STDERR {
			ch <- LogData{
				timestamp:   time.Now(),
				streamType:  t,
				payloadType: RAW,
				payload:     tmp[:n],
			}
			continue
		}

		// extract message payload
		buf.Write(tmp[:n])
		if requiredPayloadLen < 0 {
			num, err := parseContentHeader(&buf)
			if err != nil {
				ch <- LogData{
					timestamp:   time.Now(),
					streamType:  t,
					payloadType: INVALID,
					payload:     []byte(err.Error()),
				}
				continue
			}
			requiredPayloadLen = num
		}

		if buf.Len() < requiredPayloadLen {
			continue
		}

		payload := make([]byte, requiredPayloadLen)
		_, _ = buf.Read(payload)
		requiredPayloadLen = -1
		ch <- LogData{
			timestamp:   time.Now(),
			streamType:  t,
			payloadType: JSON,
			payload:     payload,
		}
	}
}

func formatEnv() string {
	sb := strings.Builder{}
	sb.Grow(1024)
	for i, env := range os.Environ() {
		if i > 0 {
			sb.WriteRune('\n')
		}
		sb.WriteString(env)
	}
	return sb.String()
}

func Run(name string, args []string, logWriter io.Writer) {
	ch := make(chan LogData, 32)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go record(ctx, ch, logWriter)

	sendMessage(STDERR, fmt.Sprintf("run: %s %s", name, args), ch)
	sendMessage(STDERR, formatEnv(), ch)

	cmd := exec.Command(name, args...)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		log.Fatalln(fmt.Errorf("failed to open stdin pipe: %v", err))
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		log.Fatalln(fmt.Errorf("failed to open stdout pipe: %v", err))
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		log.Fatalln(fmt.Errorf("failed to open stderr pipe: %v", err))
	}
	defer func() {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
	}()
	go intercept(ctx, STDIN, os.Stdin, stdinPipe, ch)
	go intercept(ctx, STDOUT, stdoutPipe, os.Stdout, ch)
	go intercept(ctx, STDERR, stderrPipe, os.Stderr, ch)
	err = cmd.Start()
	if err != nil {
		logError(fmt.Errorf("failed to start command: %v", err).Error(), ch)
		return
	}
	if err := cmd.Wait(); err != nil {
		logError(fmt.Errorf("failed to wait command: %v", err).Error(), ch)
	}
	sendMessage(STDERR, fmt.Sprintf("command exited with: %d", cmd.ProcessState.ExitCode()), ch)
	time.Sleep(100 * time.Millisecond)
}
