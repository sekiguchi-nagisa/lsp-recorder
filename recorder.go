package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func logError(err error, ch chan<- LogData) {
	value := err.Error()
	sendMessage(STDERR, value, ch)
	_, _ = os.Stderr.WriteString(value)
}

type ContentHeaderParserState int

const (
	INITIAL ContentHeaderParserState = iota
	IN_HEADER
	IN_LENGTH
	IN_NEWLINES
)

type ContentHeaderParser struct {
	state ContentHeaderParserState
	pos   int
	sb    strings.Builder
}

func NewContentHeaderParser() *ContentHeaderParser {
	c := ContentHeaderParser{}
	c.reset()
	return &c
}

func (p *ContentHeaderParser) reset() {
	p.state = INITIAL
	p.pos = 0
	p.sb.Reset()
}

func (p *ContentHeaderParser) Parse(buffer *bytes.Buffer) (int, error) {
START:
	switch p.state {
	case INITIAL, IN_HEADER:
		p.state = IN_HEADER
		header := []byte("Content-Length: ")
		for ; p.pos < len(header); p.pos++ {
			r, e := buffer.ReadByte()
			p.sb.WriteByte(r)
			if e != nil && errors.Is(e, io.EOF) {
				return -1, e // suspend
			}
			if r != header[p.pos] || e != nil {
				p.reset()
				return -1, fmt.Errorf("invalid message header: '%s'", buffer.String())
			}
		}
		p.state = IN_LENGTH
		p.pos = 0
		p.sb.Reset()
		goto START
	case IN_LENGTH:
		for {
			r, e := buffer.ReadByte()
			if e != nil {
				if errors.Is(e, io.EOF) {
					return -1, e // suspend
				}
				p.reset()
				return -1, errors.New("content length must be end with \\r\\n\\r\\n")
			}
			if r == '\r' {
				break
			}
			p.sb.WriteByte(r)
		}
		p.state = IN_NEWLINES
		p.pos = 0
		goto START
	case IN_NEWLINES:
		newlines := []byte("\n\r\n")
		for ; p.pos < len(newlines); p.pos++ {
			if r, e := buffer.ReadByte(); e != nil || r != newlines[p.pos] {
				if e != nil && errors.Is(e, io.EOF) {
					return -1, e // suspend
				}
				p.reset()
				return -1, errors.New("content length must be end with \\r\\n\\r\\n")
			}
		}
		n, e := strconv.Atoi(p.sb.String())
		p.reset()
		if e != nil {
			return -1, e
		}
		if n <= 0 {
			return -1, errors.New("content length must be greater than 0")
		}
		return n, nil
	}
	p.reset()
	return -1, io.EOF
}

func intercept(ctx context.Context, t StreamType, reader io.Reader, writer io.Writer, ch chan<- LogData) {
	chParser := NewContentHeaderParser()
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
			num, err := chParser.Parse(&buf)
			if err != nil {
				if err != io.EOF {
					ch <- LogData{
						timestamp:   time.Now(),
						streamType:  t,
						payloadType: INVALID,
						payload:     []byte(err.Error()),
					}
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
	defer func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	go record(ctx, ch, logWriter)

	sendMessage(STDERR, fmt.Sprintf("run: %s %s", name, args), ch)
	sendMessage(STDERR, formatEnv(), ch)

	cmd := exec.Command(name, args...)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		logError(fmt.Errorf("failed to open stdin pipe: %v", err), ch)
		return
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		logError(fmt.Errorf("failed to open stdout pipe: %v", err), ch)
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		logError(fmt.Errorf("failed to open stderr pipe: %v", err), ch)
		return
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
		logError(fmt.Errorf("failed to start command: %v", err), ch)
		return
	}
	if err := cmd.Wait(); err != nil {
		logError(fmt.Errorf("failed to wait command: %v", err), ch)
		return
	}
	sendMessage(STDERR, fmt.Sprintf("command exited with: %d", cmd.ProcessState.ExitCode()), ch)
}
