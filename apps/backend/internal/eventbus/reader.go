package eventbus

import "bytes"

// newLineReader lets a stream decoder walk newline-delimited JSON.
func newLineReader(raw []byte) *bytes.Reader { return bytes.NewReader(raw) }
