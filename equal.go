package jsonschema

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"math/big"
)

// Equal reports whether two JSON values are structurally equivalent
// under JSON Schema's "equal" relation: numbers compare by mathematical
// value (1 == 1.0), objects ignore key order, arrays preserve order,
// and string / bool / null compare as their JSON values. Used by the
// const, enum, and uniqueItems keywords. Empty inputs (both empty)
// compare equal; mixed-empty compares unequal. A non-nil error
// indicates malformed JSON.
func Equal(a, b []byte) (bool, error) {
	if len(a) == 0 && len(b) == 0 {
		return true, nil
	}
	if len(bytes.TrimSpace(a)) == 0 || len(bytes.TrimSpace(b)) == 0 {
		return false, nil
	}
	ad := jsontext.NewDecoder(bytes.NewBuffer(a))
	bd := jsontext.NewDecoder(bytes.NewBuffer(b))

	return equal(a, b, ad, bd)
}

func equal(a, b []byte, ad, bd *jsontext.Decoder) (bool, error) {
loop:
	for {
		ak := ad.PeekKind()
		bk := bd.PeekKind()
		if ak != bk {
			return false, nil
		}

		switch ak {
		case jsontext.KindBeginObject:
			eq, err := equalObjects(ad, bd)
			if err != nil {
				return false, err
			}
			if !eq {
				return false, nil
			}
		case jsontext.KindNumber:
			av, aErr := ad.ReadValue()
			bv, bErr := bd.ReadValue()
			if br, err := checkReadErrs(aErr, bErr); err != nil {
				return false, err
			} else if br {
				break loop
			}
			an, bn := new(big.Rat), new(big.Rat)
			an, aOk := an.SetString(string(av))
			bn, bOk := bn.SetString(string(bv))
			if !aOk {
				return false, errors.New("failed to parse number")
			}
			if !bOk {
				return false, errors.New("failed to parse number")
			}

			if an.Cmp(bn) != 0 {
				return false, nil
			}
		default:
			at, aErr := ad.ReadToken()
			bt, bErr := bd.ReadToken()
			if br, err := checkReadErrs(aErr, bErr); err != nil {
				return false, err
			} else if br {
				break loop
			}
			switch at.Kind() {
			case jsontext.KindNull:
			case jsontext.KindString:
				if at.String() != bt.String() {
					return false, nil
				}
			case jsontext.KindTrue, jsontext.KindFalse:
				if at.Bool() != bt.Bool() {
					return false, nil
				}
			}
		}

		if ad.StackDepth() == 0 && bd.StackDepth() == 0 {
			break loop
		}
	}

	if hasTrailing(a, ad.InputOffset()) || hasTrailing(b, bd.InputOffset()) {
		return false, nil
	}

	return true, nil
}

func hasTrailing(src []byte, off int64) bool {
	for _, c := range src[off:] {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return true
		}
	}
	return false
}

func checkReadErrs(aErr, bErr error) (bool, error) {
	if aErr != nil || bErr != nil {
		aEOF := errors.Is(aErr, io.EOF)
		bEOF := errors.Is(bErr, io.EOF)
		if aEOF && bEOF {
			return true, nil
		}
		if aEOF && bErr == nil || (bEOF && aErr == nil) {
			return false, nil
		}
		return false, errors.Join(aErr, bErr)
	}
	return false, nil
}

func equalObjects(ad, bd *jsontext.Decoder) (bool, error) {
	am, err := objectMembers(ad)
	if err != nil {
		return false, err
	}
	bm, err := objectMembers(bd)
	if err != nil {
		return false, err
	}
	if len(am) != len(bm) {
		return false, nil
	}
	for k, av := range am {
		bv, ok := bm[k]
		if !ok {
			return false, nil
		}
		eq, err := equal(av, bv, jsontext.NewDecoder(bytes.NewReader(av), ad.Options()), jsontext.NewDecoder(bytes.NewReader(bv), bd.Options()))
		if err != nil || !eq {
			return eq, err
		}
	}
	return true, nil
}

func objectMembers(d *jsontext.Decoder) (map[string]jsontext.Value, error) {
	if _, err := d.ReadToken(); err != nil {
		return nil, err
	}
	m := make(map[string]jsontext.Value)
	for d.PeekKind() != jsontext.KindEndObject {
		kt, err := d.ReadToken()
		if err != nil {
			return nil, err
		}
		key := kt.String()
		val, err := d.ReadValue()
		if err != nil {
			return nil, err
		}
		if _, dup := m[key]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		m[key] = bytes.Clone(val)
	}
	return m, nil
}
