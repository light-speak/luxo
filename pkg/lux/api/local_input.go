package api

import (
	"fmt"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

// Only the request envelope is prepared here. Generated local handlers own the
// complete typed validation, before entering business logic or response caching.
func (r *APIRegistry) prepareLocalJSONRequest(req *Request) error {
	req.clearPreparedJSON()
	definition, err := r.localJSONDefinition(req.API)
	if err != nil {
		return err
	}
	applyPaginationDefaults(req, definition)
	if err := validateKnownJSONParams(req, r.paramOrder[req.API]); err != nil {
		return err
	}
	return validateRequiredParams(req, definition)
}

func (r *APIRegistry) localJSONDefinition(name string) (*schema.API, error) {
	definition := r.apiDefinition(name)
	if _, registered := r.nameToID[name]; !registered || definition == nil {
		return nil, fmt.Errorf("local input schema unavailable for API %s", name)
	}
	if len(r.paramOrder[name]) > len(Request{}.paramSlots) {
		return nil, fmt.Errorf("too many params (max %d) for API %s", len(Request{}.paramSlots), name)
	}
	return definition, nil
}

// ParamInput provides a borrowed JSON input for generated typed readers. It
// distinguishes missing from explicit null without converting to a binary value.
// The reader must validate the complete value and own any data it retains.
func (r *Request) ParamInput(name string, optional, nullable bool) ([]byte, bool, error) {
	if r.Params == nil {
		return nil, false, InvalidParam(name, fmt.Errorf("expected JSON input"))
	}
	raw, present := r.Params[name]
	if !present {
		if optional {
			return nil, false, nil
		}
		return nil, false, InvalidParam(name, fmt.Errorf("missing"))
	}
	if isJSONNull(raw) {
		if nullable {
			return nil, true, nil
		}
		return nil, true, InvalidParam(name, fmt.Errorf("must not be null"))
	}
	return raw, true, nil
}
