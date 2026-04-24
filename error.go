package jsonschema

import (
	"bytes"
	"fmt"
)

type ErrorWithPosition struct {
	Name   string
	Line   int64
	Column int64
	Offset int64
	err    error
}

func (err ErrorWithPosition) Error() string {
	return fmt.Sprintf("%s:%d:%d: %s", err.Name, err.Line, err.Column, err.err)
}

func (err ErrorWithPosition) Unwrap() error { return err.err }

func NewErrorWithPosition(name string, in []byte, offset int64, err error) error {
	byteNum := int64(bytes.LastIndexByte(in[:offset], '\n'))
	if byteNum == -1 {
		byteNum = offset // On first line.
	} else {
		byteNum++ // After the newline.
		byteNum = offset - byteNum
	}
	lineNum := 1 + int64(bytes.Count(in[:offset], []byte("\n")))
	return ErrorWithPosition{
		Name:   name,
		Line:   lineNum,
		Column: byteNum,
		Offset: offset,
		err:    err,
	}
}
