package semantic

import "testing"

const inferredParameterModel = `
model Item @noTime {
 id: Int @id
 score: Int
 enabled: Boolean
 note: String?
}
`

func TestAnalyzedFilesIncludeInheritedFieldsForModuleGeneration(t *testing.T) {
	result := analyze(t, `
model Base @noTime { id: Int @id }
model Item : Base @noTime { name: String }
api getItemById(key: Int): Item
`)
	if len(result.Errors) != 0 {
		t.Fatal(result.Errors)
	}
	item := result.Files[0].Models[1]
	if len(item.Fields) != 2 || item.Fields[1].Name != "id" {
		t.Fatal("module generation loses inherited fields when parent files are separated")
	}
}

func TestInferredParametersRejectUnusedOrMissingArguments(t *testing.T) {
	for _, source := range []string{
		`api getItem(id: Int): Item`,
		`api listItems(value: Int): [Item]`,
		`api getItemById(id: Int, unused: Int): Item`,
		`api getItemByIdAndScore(id: Int): Item`,
		`api listItemsByScoreBetween(lower: Int): [Item]`,
		`api listItemsByEnabledTrue(enabled: Boolean): [Item]`,
		`api listItemsByNoteIsNull(note: String): [Item]`,
	} {
		t.Run(source, func(t *testing.T) {
			expectError(t, analyze(t, inferredParameterModel+source), "parameters but its name consumes")
		})
	}
}

func TestInferredParametersKeepPositionalAndNativeContracts(t *testing.T) {
	for _, source := range []string{
		`api getItemById(key: Int): Item`,
		`api getItemById`,
		`api listItems`,
		`api listItemsByScoreBetween(lower: Int, upper: Int): [Item]`,
		`api listItemsByIdOrScore(key: Int, score: Int): [Item]`,
		`api listItemsByIdIn(ids: [Int]): [Item]`,
		`api getItem(id: Int): Item @native`,
		`api getItem(id: Int): Item { return Item.find(id: id) }`,
	} {
		t.Run(source, func(t *testing.T) { expectNoErrors(t, analyze(t, inferredParameterModel+source)) })
	}
}

func TestInferredParametersRejectMismatchedTypes(t *testing.T) {
	for _, source := range []string{
		`api getItemById(id: String): Item`,
		`api listItemsByIdIn(ids: Int): [Item]`,
		`api listItemsByScoreBetween(lower: Int, upper: String): [Item]`,
		`api getItemById(id: Int?): Item`,
	} {
		t.Run(source, func(t *testing.T) { expectError(t, analyze(t, inferredParameterModel+source), "inferred parameter") })
	}
}

func TestInferredParametersValidateInheritedFields(t *testing.T) {
	source := `model Base @noTime { id: Int @id }
model Item : Base @noTime { name: String }
api getItemById(key: String): Item`
	expectError(t, analyze(t, source), "inferred parameter")
}
