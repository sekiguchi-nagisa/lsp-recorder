package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
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
	JSON PayloadType = iota
	RAW
	STRING
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
			_, _ = fmt.Fprintf(writer, "%s %s ", v.timestamp.Format(time.RFC3339), toString(v.streamType))
			_, _ = writer.Write(v.payload) //FIXME: parse payload
			_, _ = writer.Write([]byte("\n"))
		}
	}
}

func sendMessage(t StreamType, value string, ch chan<- LogData) {
	ch <- LogData{
		timestamp:   time.Now(),
		streamType:  t,
		payloadType: STRING,
		payload:     []byte(value),
	}
}

func logError(value string, ch chan<- LogData) {
	sendMessage(STDERR, value, ch)
	_, _ = os.Stderr.WriteString(value)
}

func intercept(ctx context.Context, t StreamType, reader io.Reader, writer io.Writer, ch chan<- LogData) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		data := make([]byte, 1024)
		n, _ := reader.Read(data)     //FIXME: read error handling
		n, _ = writer.Write(data[:n]) //FIXME: write error handling
		ch <- LogData{                //FIXME: parse json
			timestamp:   time.Now(),
			streamType:  t,
			payloadType: RAW,
			payload:     data[:n],
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
	stdinPipe, _ := cmd.StdinPipe()   //FIXME: check error
	stdoutPipe, _ := cmd.StdoutPipe() //FIXME
	stderrPipe, _ := cmd.StderrPipe() //FIXME
	defer func() {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
	}()
	go intercept(ctx, STDIN, os.Stdin, stdinPipe, ch)
	go intercept(ctx, STDOUT, stdoutPipe, os.Stdout, ch)
	go intercept(ctx, STDERR, stderrPipe, os.Stderr, ch)
	err := cmd.Start()
	if err == nil {
		err = cmd.Wait()
	}
	if err != nil {
		go logError(err.Error(), ch) //FIXME: more precise error message
	}
}
