package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type StreamType int

const (
	STDIN StreamType = iota
	STDOUT
	STDERR
)

func (s StreamType) String() string {
	switch s {
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

func (s *StreamType) UnmarshalJSON(i []byte) error {
	switch string(i) {
	case `"<stdin>"`:
		*s = STDIN
	case `"<stdout>"`:
		*s = STDOUT
	case `"<stderr>"`:
		*s = STDERR
	default:
		return errors.New("invalid stream type: " + string(i))
	}
	return nil
}

type PayloadType int

const (
	INVALID PayloadType = iota // for invalid LSP message
	JSON
	RAW
	RAW_START
	RAW_END
)

func (t PayloadType) String() string {
	switch t {
	case INVALID:
		return "invalid"
	case JSON:
		return "json"
	case RAW:
		return "raw"
	case RAW_START:
		return "start"
	case RAW_END:
		return "end"
	default:
		return ""
	}
}

func (t *PayloadType) UnmarshalJSON(i []byte) error {
	switch string(i) {
	case `"invalid"`:
		*t = INVALID
	case `"json"`:
		*t = JSON
	case `"raw"`:
		*t = RAW
	case `"start"`:
		*t = RAW_START
	case `"end"`:
		*t = RAW_END
	default:
		return errors.New("invalid payload type: " + string(i))
	}
	return nil
}

type LogData struct {
	Timestamp   time.Time   `json:"timestamp"`
	StreamType  StreamType  `json:"type"`
	PayloadType PayloadType `json:"payload"`
	Payload     string      `json:"msg"`
}

func (l *LogData) String() string {
	builder := strings.Builder{}
	builder.WriteString(l.Timestamp.Format("2006-01-02 15:04:05.000-07"))
	builder.WriteString(" ")
	builder.WriteString(l.StreamType.String())
	builder.WriteString(" ")
	builder.WriteString(l.PayloadType.String())
	builder.WriteString(": ")
	if l.PayloadType == JSON {
		buf := bytes.NewBuffer(nil)
		err := json.Indent(buf, []byte(l.Payload), "", "  ")
		if err != nil {
			return ""
		}
		builder.WriteString("\n")
		builder.Write(buf.Bytes())
	} else {
		builder.WriteString(l.Payload)
	}
	return builder.String()
}

func record(ctx context.Context, ch <-chan LogData, logger *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case v := <-ch:
			logger.Info(v.Payload, "timestamp", v.Timestamp.Format(time.RFC3339Nano),
				"type", v.StreamType.String(), "payload", v.PayloadType.String())
		}
	}
}

func sendMessage(t StreamType, p PayloadType, value string, ch chan<- LogData) {
	ch <- LogData{
		Timestamp:   time.Now(),
		StreamType:  t,
		PayloadType: p,
		Payload:     value,
	}
}

func logError(logger *slog.Logger, err error) {
	logger.Error(err.Error(), "type", STDERR.String(), "payload", RAW_END.String())
	value := err.Error()
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

		if t == STDERR {
			ch <- LogData{
				Timestamp:   time.Now(),
				StreamType:  t,
				PayloadType: RAW,
				Payload:     string(tmp[:n]),
			}
			_, _ = writer.Write(tmp[:n]) //FIXME: write error handling
			continue
		}

		// extract message payload
		buf.Write(tmp[:n])
		if requiredPayloadLen < 0 {
			num, err := chParser.Parse(&buf)
			if err != nil {
				if err != io.EOF {
					ch <- LogData{
						Timestamp:   time.Now(),
						StreamType:  t,
						PayloadType: INVALID,
						Payload:     err.Error(),
					}
					_, _ = writer.Write(tmp[:n]) //FIXME: write error handling
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
			Timestamp:   time.Now(),
			StreamType:  t,
			PayloadType: JSON,
			Payload:     string(payload),
		}
		_, _ = fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n", len(payload))
		_, _ = writer.Write(payload) //FIXME: write error handling
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

func Run(name string, args []string, logger *slog.Logger) {
	ch := make(chan LogData, 32)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer func() {
		time.Sleep(100 * time.Millisecond)
		stop()
	}()
	go record(ctx, ch, logger)

	sendMessage(STDERR, RAW_START, fmt.Sprintf("run: %s %s", name, args), ch)
	sendMessage(STDERR, RAW, formatEnv(), ch)

	cmd := exec.Command(name, args...)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		logError(logger, fmt.Errorf("failed to open stdin pipe: %v", err))
		return
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		logError(logger, fmt.Errorf("failed to open stdout pipe: %v", err))
		return
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		logError(logger, fmt.Errorf("failed to open stderr pipe: %v", err))
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
		logError(logger, fmt.Errorf("failed to start command: %v", err))
		return
	}
	if err := cmd.Wait(); err != nil {
		logError(logger, fmt.Errorf("failed to wait command: %v", err))
		return
	}
	sendMessage(STDERR, RAW_END, fmt.Sprintf("command exited with: %d", cmd.ProcessState.ExitCode()), ch)
}
