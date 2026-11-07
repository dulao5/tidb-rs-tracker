package vec

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"unicode/utf8"
)

// GoString is a Go string in a record. Go strings are byte strings; JSON
// strings are Unicode, and encoding/json silently replaces invalid UTF-8 with
// U+FFFD. A GoString that is valid UTF-8 encodes as a JSON string; otherwise
// as {"hex": "<bytes>"}. Replayers must accept both forms.
//
// Every string in a record (args, out, err.msg) must be valid UTF-8 or a
// GoString: Run panics on a plain string that is not (CheckUTF8), so no domain
// can lose bytes unnoticed.
type GoString string

// MarshalJSON implements json.Marshaler.
func (s GoString) MarshalJSON() ([]byte, error) {
	if utf8.ValidString(string(s)) {
		return json.Marshal(string(s))
	}
	return json.Marshal(map[string]string{"hex": hex.EncodeToString([]byte(s))})
}

// UnmarshalJSON implements json.Unmarshaler (both forms).
func (s *GoString) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err == nil {
		*s = GoString(str)
		return nil
	}
	var h struct {
		Hex string `json:"hex"`
	}
	if err := json.Unmarshal(b, &h); err != nil {
		return err
	}
	raw, err := hex.DecodeString(h.Hex)
	if err != nil {
		return err
	}
	*s = GoString(raw)
	return nil
}

var goStringType = reflect.TypeOf(GoString(""))

// CheckUTF8 reports a string reachable from v that is not valid UTF-8 and is
// not a GoString (it would be mangled by encoding/json).
func CheckUTF8(v any) error {
	return checkValue(reflect.ValueOf(v), "out")
}

func checkValue(v reflect.Value, path string) error {
	if !v.IsValid() {
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		if v.Type() != goStringType && !utf8.ValidString(v.String()) {
			return fmt.Errorf("govec: %s: non-UTF-8 string %q; use vec.GoString", path, v.String())
		}
	case reflect.Pointer, reflect.Interface:
		return checkValue(v.Elem(), path)
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return nil // []byte: encoding/json uses base64
		}
		for i := 0; i < v.Len(); i++ {
			if err := checkValue(v.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.Map:
		it := v.MapRange()
		for it.Next() {
			if err := checkValue(it.Key(), path+".key"); err != nil {
				return err
			}
			if err := checkValue(it.Value(), path+"."+fmt.Sprint(it.Key())); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				if err := checkValue(v.Field(i), path+"."+v.Type().Field(i).Name); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
