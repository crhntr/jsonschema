package jsonhandler

import (
	"os"
	"testing"
)

func TestSmoke(t *testing.T) {
	if os.Getenv("SMOKE") == "" {
		t.Skip()
	}
	h := newHandler(t, `{"title":"Person","type":"object","required":["name"],"properties":{"name":{"type":"string","minLength":1},"age":{"type":"integer","minimum":0},"email":{"type":"string","format":"email"},"tags":{"type":"array","items":{"type":"string"}},"pet":{"oneOf":[{"title":"Dog","type":"object","properties":{"kind":{"const":"dog"},"barks":{"type":"boolean"}},"required":["kind"]},{"title":"Cat","type":"object","properties":{"kind":{"const":"cat"}},"required":["kind"]}]}}}`)
	t.Log(readBody(t, getForm(t, h)))
	m := metaHandler(t)
	t.Log(readBody(t, getForm(t, m)))
}
