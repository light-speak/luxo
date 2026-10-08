package auth

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/jsonutil"
)

func readTokenClaims(payload []byte, purpose string) (Claims, int64, error) {
	reader := jsonutil.BorrowReader(payload)
	defer reader.Release()
	start, err := reader.ReadToken()
	if err != nil {
		return Claims{}, 0, err
	}
	if start.Kind() != '{' {
		return Claims{}, 0, fmt.Errorf("JWT payload must be an object")
	}
	var claims Claims
	var expiresAt int64
	var seen uint8
	for {
		key, err := reader.ReadToken()
		if err != nil {
			return Claims{}, 0, err
		}
		if key.Kind() == '}' {
			break
		}
		switch key.String() {
		case "data":
			claims, err = readClaimsObject(reader, payload)
			seen |= 1
		case "exp":
			expiresAt, err = readClaimTimestamp(reader)
			seen |= 2
		case "iat":
			_, err = readClaimTimestamp(reader)
		case "token_use":
			value, readErr := reader.ReadToken()
			if readErr != nil {
				return Claims{}, 0, readErr
			}
			if value.Kind() != '"' || value.String() != purpose {
				return Claims{}, 0, fmt.Errorf("unexpected JWT token purpose")
			}
			seen |= 4
		default:
			_, err = reader.ReadValue()
		}
		if err != nil {
			return Claims{}, 0, err
		}
	}
	if err := finishClaimsObject(reader, payload); err != nil {
		return Claims{}, 0, err
	}
	if seen&7 != 7 {
		return Claims{}, 0, fmt.Errorf("JWT data, exp and token_use are required")
	}
	return claims, expiresAt, nil
}

func readClaimTimestamp(reader *jsonutil.Reader) (int64, error) {
	value, err := reader.ReadValue()
	if err != nil {
		return 0, err
	}
	return jsonutil.ParseInt64(value)
}

// AppendJSON reuses the verified immutable object during token renewal.
func (c Claims) AppendJSON(dst []byte) ([]byte, error) {
	if c.data == nil {
		return dst, fmt.Errorf("JWT claims are required")
	}
	return append(dst, c.data.raw...), nil
}
