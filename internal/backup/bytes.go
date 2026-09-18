package backup

import "bytes"

func newByteReader(data []byte) *bytes.Reader { return bytes.NewReader(data) }
