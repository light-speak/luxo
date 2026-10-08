package lockfile

// Member bits only track JSON metadata keys; they are not protocol field IDs.
type lockMember uint16

const (
	lockVersion lockMember = 1 << iota
	lockModels
	lockTypes
	lockEvents
	lockAPIs
	lockReservedAPIs
	lockNextID
	lockFields
	lockFieldTypes
	lockReserved
	lockID
	lockParams
	lockParamTypes
	lockReturnType
	lockContract
)

// The token text is compared before advancing the decoder. Only names stored
// in the resulting maps need owned strings.
func lockMemberFor(name string) lockMember {
	switch name {
	case "version":
		return lockVersion
	case "models":
		return lockModels
	case "types":
		return lockTypes
	case "events":
		return lockEvents
	case "apis":
		return lockAPIs
	case "reserved_api_ids":
		return lockReservedAPIs
	case "next_id":
		return lockNextID
	case "fields":
		return lockFields
	case "field_types":
		return lockFieldTypes
	case "reserved":
		return lockReserved
	case "id":
		return lockID
	case "params":
		return lockParams
	case "param_types":
		return lockParamTypes
	case "return_type":
		return lockReturnType
	case "contract":
		return lockContract
	default:
		return 0
	}
}
