package repoconfig

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/openconfig/ygot/ygot"
)

type (
	ExportFormat string
)

const (
	JSON ExportFormat = "JSON"
	YAML ExportFormat = "YAML"
)

type YangObject interface {
	IsYANGGoStruct()
}

// need to put this here because it's imported from several packages and would otherwise
// create a circular dependency
type RepoConfig struct {
	Path    string
	Binding YangObject
	Raw     interface{}
}

func (rcs RepoConfig) Serialize(exportFormat ExportFormat) string {
	// Serialize RepoConfig object to YAML or JSON. If there is a binding, then we
	// get JSON from the binding, wrap it in the path, then export. If there is no
	// binding then we assume Raw contains the structure we need, and so wrap that in
	// the path and export.
	var parentContainer map[string]interface{}
	if rcs.Binding != nil {
		jsonString, jsonErr := ygot.EmitJSON(
			rcs.Binding, &ygot.EmitJSONConfig{
				Format: ygot.RFC7951,
				Indent: "",
				RFC7951Config: &ygot.RFC7951JSONConfig{
					AppendModuleName: true,
				},
			},
		)
		if jsonErr != nil {
			panic(fmt.Sprintf("JSON export error: %v", jsonErr))
		}
		container := make(map[string]interface{})
		json.Unmarshal([]byte(jsonString), &container)
		parentContainer = map[string]interface{}{rcs.Path: container}
	} else {
		parentContainer = map[string]interface{}{rcs.Path: rcs.Raw}
	}
	if exportFormat == YAML {
		var out bytes.Buffer
		encoder := yaml.NewEncoder(&out)
		encoder.SetIndent(2)
		encoder.Encode(parentContainer)
		return out.String()
	} else if exportFormat == JSON {
		// hard code jsonIndent to true for now
		jsonIndent := true
		var jsonBytes []byte
		var err error
		if jsonIndent {
			jsonBytes, err = json.MarshalIndent(parentContainer, "", "  ")
		} else {
			jsonBytes, err = json.Marshal(parentContainer)
		}
		if err != nil {
			fmt.Println(err)
		}
		return string(jsonBytes)
	} else {
		panic("Unknown export format: " + string(exportFormat))
	}
}
