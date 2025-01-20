package recorder

import (
	"bytes"
	"fmt"
	"github.com/stretchr/testify/assert"
	"io"
	"testing"
)

func TestParserSuccess(t *testing.T) {
	hd := []byte{'C', 'o', 'n', 't', 'e', 'n', 't', '-', 'L', 'e', 'n', 'g', 't', 'h', ':', ' ',
		'1', '2', '3', '\r', '\n', '\r', '\n'}

	parser := NewContentHeaderParser()
	buf := bytes.Buffer{}
	for i := 0; i < len(hd); i++ {
		buf.WriteByte(hd[i])
		n, e := parser.Parse(&buf)
		if i < len(hd)-1 {
			assert.Equal(t, -1, n, fmt.Sprintf("failed at: %d (%c)", i, hd[i]))
			assert.ErrorIs(t, e, io.EOF)
		} else {
			assert.Equal(t, 123, n, fmt.Sprintf("failed at: %d (%c)", i, hd[i]))
			assert.NoError(t, e)
		}
	}
}
