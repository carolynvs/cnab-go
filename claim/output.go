package claim

import (
	"sort"

	"github.com/cnabio/cnab-go/bundle"
	"github.com/cnabio/cnab-go/bundle/definition"
	"github.com/cnabio/cnab-go/storage"
)

var _ storage.Document = Output{}

// Output represents a bundle output generated by an operation.
type Output struct {
	// Claim fo the operation that generated the output.
	claim Claim

	// Result of the operation that generated the output.
	result Result

	// Name of the output.
	Name string

	// Value of the output persisted to storage.
	Value []byte
}

// NewOutput creates a new Output document with all required values set.
func NewOutput(c Claim, r Result, name string, value []byte) Output {
	r.claim = &c
	return Output{
		claim:  c,
		result: r,
		Name:   name,
		Value:  value,
	}
}

func (o Output) GetGroup() string {
	return o.result.ID
}

func (o Output) GetNamespace() string {
	return o.result.Namespace
}
func (o Output) GetName() string {
	return o.result.ID + "-" + o.Name
}

func (o Output) GetType() string {
	panic("implement me")
}

func (o Output) ShouldEncrypt() bool {
	sensitive, _ := o.claim.Bundle.IsOutputSensitive(o.Name)
	return sensitive
}

func (o Output) GetData() ([]byte, error) {
	return o.Value, nil
}

// GetDefinition returns the output definition, or false if the output is not defined.
func (o Output) GetDefinition() (bundle.Output, bool) {
	def, ok := o.claim.Bundle.Outputs[o.Name]
	return def, ok
}

// GetSchema returns the schema for the output, or false if the schema is not defined.
func (o Output) GetSchema() (definition.Schema, bool) {
	if def, ok := o.GetDefinition(); ok {
		if schema, ok := o.claim.Bundle.Definitions[def.Definition]; ok {
			return *schema, ok
		}
	}

	return definition.Schema{}, false
}

type Outputs struct {
	// Sorted list of outputs
	vals []Output
	// output name -> index of the output in vals
	keys map[string]int
}

func NewOutputs(outputs []Output) Outputs {
	o := Outputs{
		vals: make([]Output, len(outputs)),
		keys: make(map[string]int, len(outputs)),
	}

	copy(o.vals, outputs)
	for i, output := range outputs {
		o.keys[output.Name] = i
	}

	sort.Sort(o)
	return o
}

func (o Outputs) GetByName(name string) (Output, bool) {
	i, ok := o.keys[name]
	if !ok || i >= len(o.vals) {
		return Output{}, false
	}

	return o.vals[i], true
}

func (o Outputs) GetByIndex(i int) (Output, bool) {
	if i < 0 || i >= len(o.vals) {
		return Output{}, false
	}

	return o.vals[i], true
}

func (o Outputs) Len() int {
	return len(o.vals)
}

func (o Outputs) Less(i, j int) bool {
	return o.vals[i].Name < o.vals[j].Name
}

func (o Outputs) Swap(i, j int) {
	o.keys[o.vals[i].Name] = j
	o.keys[o.vals[j].Name] = i
	o.vals[i], o.vals[j] = o.vals[j], o.vals[i]
}
