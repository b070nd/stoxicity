package instrument

import (
	"bytes"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// Parse reads an instrument file. Unknown keys are an error, so a misspelt
// field (a quote unit, say) is never silently ignored.
func Parse(r io.Reader) (Set, error) {
	var s Set
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		if err == io.EOF {
			return Set{}, fmt.Errorf("instruments: the file is empty")
		}
		return Set{}, fmt.Errorf("instruments: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		return Set{}, fmt.Errorf("instruments: the file holds more than one document")
	}
	return s, nil
}

// Marshal writes the set as YAML.
func Marshal(s Set) ([]byte, error) {
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return b.Bytes(), enc.Close()
}
