package lockfile

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"strconv"
)

// readLock uses the standard tokenizer for syntax and Unicode. Every object is
// read through readLockMembers or readLockMap, which reject duplicate names
// without maintaining a second dynamic directory in the tokenizer.
func readLock(data []byte) (*LockFile, error) {
	d := jsontext.NewDecoder(bytes.NewBuffer(data), jsontext.AllowDuplicateNames(true))
	lf := &LockFile{Version: currentVersion}
	err := readLockMembers(d, lockVersion|lockModels|lockTypes|lockEvents|lockAPIs|lockReservedAPIs, func(member lockMember) (err error) {
		switch member {
		case lockVersion:
			lf.Version, err = readLockInt(d)
		case lockModels:
			lf.Models, err = readLockMap(d, readModelLock)
		case lockTypes:
			lf.Types, err = readLockMap(d, readModelLock)
		case lockEvents:
			lf.Events, err = readLockMap(d, readModelLock)
		case lockAPIs:
			lf.APIs, err = readLockMap(d, readAPILock)
		case lockReservedAPIs:
			lf.ReservedAPI, err = readLockInts(d)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if _, err := d.ReadToken(); err != io.EOF {
		return nil, fmt.Errorf("luxo.lock must contain exactly one object: %v", err)
	}
	// Preserve omitted defaults without allocating maps that decoded members replace.
	if lf.Models == nil {
		lf.Models = make(map[string]*ModelLock)
	}
	if lf.Events == nil {
		lf.Events = make(map[string]*ModelLock)
	}
	if lf.APIs == nil {
		lf.APIs = make(map[string]*APILock)
	}
	return lf, nil
}

func readModelLock(d *jsontext.Decoder) (*ModelLock, error) {
	m := new(ModelLock)
	err := readLockMembers(d, lockNextID|lockFields|lockFieldTypes|lockReserved, func(member lockMember) (err error) {
		switch member {
		case lockNextID:
			m.NextID, err = readLockInt(d)
		case lockFields:
			m.Fields, err = readLockMap(d, readLockInt)
		case lockFieldTypes:
			m.FieldTypes, err = readLockMap(d, readLockString)
		case lockReserved:
			m.Reserved, err = readLockInts(d)
		}
		return err
	})
	return m, err
}

func readAPILock(d *jsontext.Decoder) (*APILock, error) {
	a := new(APILock)
	err := readLockMembers(d, lockID|lockParams|lockParamTypes|lockReturnType|lockContract|lockReserved, func(member lockMember) (err error) {
		switch member {
		case lockID:
			a.ID, err = readLockInt(d)
		case lockParams:
			a.Params, err = readLockMap(d, readLockInt)
		case lockParamTypes:
			a.ParamTypes, err = readLockMap(d, readLockString)
		case lockReturnType:
			a.ReturnType, err = readLockString(d)
		case lockContract:
			a.Contract, err = readLockBool(d)
		case lockReserved:
			a.Reserved, err = readLockInts(d)
		}
		return err
	})
	return a, err
}

func readLockMembers(d *jsontext.Decoder, allowed lockMember, visit func(lockMember) error) error {
	if _, err := readLockToken(d, '{'); err != nil {
		return err
	}
	var seen lockMember
	for d.PeekKind() != '}' {
		key, err := readLockToken(d, '"')
		if err != nil {
			return err
		}
		member := lockMemberFor(key.String())
		if member&allowed == 0 {
			return fmt.Errorf("unknown lock member %q", key.String())
		}
		if seen&member != 0 {
			return fmt.Errorf("duplicate lock member %q", key.String())
		}
		seen |= member
		if err := visit(member); err != nil {
			return err
		}
	}
	_, err := readLockToken(d, '}')
	return err
}

func readLockMap[T any](d *jsontext.Decoder, read func(*jsontext.Decoder) (T, error)) (map[string]T, error) {
	if _, err := readLockToken(d, '{'); err != nil {
		return nil, err
	}
	values := make(map[string]T)
	for d.PeekKind() != '}' {
		key, err := readLockToken(d, '"')
		if err != nil {
			return nil, err
		}
		name := key.String()
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("duplicate lock name %q", name)
		}
		value, err := read(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		values[name] = value
	}
	_, err := readLockToken(d, '}')
	return values, err
}

func readLockInts(d *jsontext.Decoder) ([]int, error) {
	if _, err := readLockToken(d, '['); err != nil {
		return nil, err
	}
	values := []int{}
	for d.PeekKind() != ']' {
		value, err := readLockInt(d)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	_, err := readLockToken(d, ']')
	return values, err
}

func readLockInt(d *jsontext.Decoder) (int, error) {
	t, err := readLockToken(d, '0')
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(t.String())
}

func readLockString(d *jsontext.Decoder) (string, error) {
	t, err := readLockToken(d, '"')
	if err != nil {
		return "", err
	}
	return t.String(), nil
}

func readLockBool(d *jsontext.Decoder) (bool, error) {
	t, err := d.ReadToken()
	if err != nil {
		return false, err
	}
	if t.Kind() != 't' && t.Kind() != 'f' {
		return false, fmt.Errorf("expected boolean, got %s", t.Kind())
	}
	return t.Kind() == 't', nil
}

func readLockToken(d *jsontext.Decoder, kind jsontext.Kind) (jsontext.Token, error) {
	t, err := d.ReadToken()
	if err != nil {
		return jsontext.Token{}, err
	}
	if t.Kind() != kind {
		return jsontext.Token{}, fmt.Errorf("expected %s, got %s", kind, t.Kind())
	}
	return t, nil
}
