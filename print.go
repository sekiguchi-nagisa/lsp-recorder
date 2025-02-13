package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

func Print(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 1024*16)
	scanner.Buffer(buf, 1024*1024*64)
	for scanner.Scan() {
		logRecord := LogData{}
		err := json.Unmarshal([]byte(scanner.Text()), &logRecord)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(writer, logRecord.String())
		if err != nil {
			return err
		}
	}
	return scanner.Err()
}
