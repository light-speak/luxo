package schema

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/light-speak/luxo/pkg/lux/codec"
	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

// SealedToJSON converts one complete tagged frame without constructing a model.
func (s *Schema) SealedToJSON(dst, data []byte, name string) ([]byte, error) {
	return s.sealedToJSON(dst, data, name, 1)
}

func (s *Schema) sealedToJSON(dst, data []byte, name string, depth int) ([]byte, error) {
	original := dst
	declaration := s.Sealeds[name]
	if declaration == nil {
		return original, fmt.Errorf("schema: unknown sealed type %q", name)
	}
	if depth >= codec.MaxModelDepth {
		return original, codec.ErrModelDepth
	}
	dec := codec.NewDecoder(data)
	if !dec.NextField() {
		dec.Fail(fmt.Errorf("schema: %s requires a nonzero variant tag", name))
	}
	variant := declaration.byID[dec.FieldID()]
	payload := dec.ReadBytes()
	if err := dec.Finish(); err != nil {
		return original, err
	}
	if variant == nil {
		return original, fmt.Errorf("schema: unknown %s variant tag %d", name, dec.FieldID())
	}
	dst = append(dst, '{')
	dst = strconv.AppendQuote(dst, variant.Name)
	dst = append(dst, ':')
	dst, err := binaryToJSON(dst, payload, variant.row, depth+1, s)
	if err != nil {
		return original, err
	}
	return append(dst, '}'), nil
}

// SealedListToJSON converts a count followed by individually framed sum values.
func (s *Schema) SealedListToJSON(dst, data []byte, name string) ([]byte, error) {
	return s.sealedListToJSON(dst, data, name, 1)
}

func (s *Schema) sealedListToJSON(dst, data []byte, name string, depth int) ([]byte, error) {
	original := dst
	if s.Sealeds[name] == nil {
		return original, fmt.Errorf("schema: unknown sealed type %q", name)
	}
	dec := codec.NewDecoder(data)
	dst, err := s.appendSealedListJSON(dst, dec, name, depth)
	if err == nil {
		err = dec.Finish()
	}
	if err != nil {
		return original, err
	}
	return dst, nil
}

func (s *Schema) appendSealedListJSON(dst []byte, dec *codec.Decoder, name string, depth int) ([]byte, error) {
	count := dec.ReadArrayLength()
	if !dec.CheckElementBytes(count, 1) {
		return dst, dec.Err()
	}
	dst = append(dst, '[')
	for index := range count {
		if index != 0 {
			dst = append(dst, ',')
		}
		raw := dec.ReadBytes()
		if dec.Err() != nil {
			return dst, dec.Err()
		}
		var err error
		dst, err = s.sealedToJSON(dst, raw, name, depth)
		if err != nil {
			return dst, err
		}
	}
	return append(dst, ']'), nil
}

func (s *Schema) writeJSONSealed(enc *codec.Encoder, declaration *Sealed, raw []byte, depth int) error {
	if depth >= codec.MaxModelDepth {
		return codec.ErrModelDepth
	}
	variant, payload, err := sealedJSONVariant(declaration, raw)
	if err != nil {
		return err
	}
	values, err := s.jsonObjectValues(variant.row, payload, depth+1)
	if err != nil {
		return err
	}
	for _, field := range variant.Fields {
		if _, present := values[field.Name]; !present && !field.HasDefault {
			return fmt.Errorf("schema: %s.%s missing required field %q", declaration.Name, variant.Name, field.Name)
		}
	}
	var body codec.Encoder
	if err := s.writeJSONObjectValues(&body, variant.row, values, depth+1); err != nil {
		return err
	}
	enc.WriteVarint(uint64(variant.ID))
	enc.WriteBytes(body.Bytes())
	return nil
}

func sealedJSONVariant(declaration *Sealed, raw []byte) (*Variant, []byte, error) {
	var variant *Variant
	var payload []byte
	err := jsonutil.VisitObject(raw, func(name, value []byte) error {
		if payload != nil {
			return fmt.Errorf("schema: %s requires exactly one variant", declaration.Name)
		}
		variant = declaration.byName[string(name)]
		if variant == nil {
			return fmt.Errorf("schema: unknown %s variant %q", declaration.Name, name)
		}
		payload = value
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if variant == nil {
		return nil, nil, fmt.Errorf("schema: %s requires exactly one variant", declaration.Name)
	}
	return variant, payload, nil
}

func (s *Schema) writeJSONSealedField(enc *codec.Encoder, field *Field, raw json.RawMessage, depth int) error {
	declaration := s.Sealeds[field.TypeName]
	if declaration == nil {
		return fmt.Errorf("schema: unknown sealed type %q", field.TypeName)
	}
	var child codec.Encoder
	if err := s.writeJSONSealed(&child, declaration, raw, depth+1); err != nil {
		return err
	}
	enc.WriteBytes(child.Bytes())
	return nil
}

func appendSealedFieldJSON(dst []byte, dec *codec.Decoder, field *Field, depth int, s *Schema) ([]byte, error) {
	if field.Nullable && !dec.ReadBool() {
		return append(dst, "null"...), dec.Err()
	}
	if s == nil {
		return dst, fmt.Errorf("schema: sealed field %q requires schema", field.Name)
	}
	if field.IsList {
		return s.appendSealedListJSON(dst, dec, field.TypeName, depth+1)
	}
	raw := dec.ReadBytes()
	if dec.Err() != nil {
		return dst, dec.Err()
	}
	return s.sealedToJSON(dst, raw, field.TypeName, depth+1)
}

// Earlier duplicate values retain resource limits without business decoding.
func (s *Schema) checkJSONSealedDepth(reader *jsonutil.DepthReader, declaration *Sealed, depth int) error {
	if declaration == nil || reader.Kind() != '{' {
		return reader.JSON()
	}
	if depth >= codec.MaxModelDepth {
		return codec.ErrModelDepth
	}
	return reader.Object(func(name []byte) error {
		variant := declaration.byName[string(name)]
		if variant == nil || reader.Kind() != '{' {
			return reader.JSON()
		}
		return reader.Object(func(name []byte) error {
			field := lookupJSONField(variant.row, string(name))
			if field == nil {
				return reader.JSON()
			}
			return s.checkJSONFieldDepth(reader, field, depth+1)
		})
	})
}
