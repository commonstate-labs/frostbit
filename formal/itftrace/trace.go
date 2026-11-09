                                                                           
                                                                       
                                                                            
package itftrace

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

type Trace struct {
	States []State `json:"states"`
}

type State struct {
	Action string                     `json:"mbt::actionTaken"`
	Values map[string]json.RawMessage `json:"state"`
}

func Load(path string) (*Trace, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var trace Trace
	if err := json.Unmarshal(data, &trace); err != nil {
		return nil, err
	}
	if len(trace.States) == 0 {
		return nil, fmt.Errorf("ITF trace has no states")
	}
	return &trace, nil
}

func (s State) Int(name string) (int64, error) {
	raw, ok := s.Values[name]
	if !ok {
		return 0, fmt.Errorf("ITF state has no %q field", name)
	}
	var encoded struct {
		BigInt string `json:"#bigint"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return 0, fmt.Errorf("decode ITF integer %q: %w", name, err)
	}
	if encoded.BigInt == "" {
		return 0, fmt.Errorf("ITF field %q is not an integer", name)
	}
	value, err := strconv.ParseInt(encoded.BigInt, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ITF integer %q: %w", name, err)
	}
	return value, nil
}

func (s State) Bool(name string) (bool, error) {
	raw, ok := s.Values[name]
	if !ok {
		return false, fmt.Errorf("ITF state has no %q field", name)
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, fmt.Errorf("decode ITF boolean %q: %w", name, err)
	}
	return value, nil
}

func (s State) Tag(name string) (string, error) {
	raw, ok := s.Values[name]
	if !ok {
		return "", fmt.Errorf("ITF state has no %q field", name)
	}
	var encoded struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return "", fmt.Errorf("decode ITF variant %q: %w", name, err)
	}
	if encoded.Tag == "" {
		return "", fmt.Errorf("ITF field %q is not a variant", name)
	}
	return encoded.Tag, nil
}

func (s State) SetTags(name string) (map[string]struct{}, error) {
	raw, ok := s.Values[name]
	if !ok {
		return nil, fmt.Errorf("ITF state has no %q field", name)
	}
	var encoded struct {
		Values []struct {
			Tag string `json:"tag"`
		} `json:"#set"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode ITF set %q: %w", name, err)
	}
	result := make(map[string]struct{}, len(encoded.Values))
	for _, value := range encoded.Values {
		if value.Tag == "" {
			return nil, fmt.Errorf("ITF set %q contains a non-variant value", name)
		}
		result[value.Tag] = struct{}{}
	}
	return result, nil
}

func (s State) SetInts(name string) (map[int64]struct{}, error) {
	raw, ok := s.Values[name]
	if !ok {
		return nil, fmt.Errorf("ITF state has no %q field", name)
	}
	var encoded struct {
		Values []struct {
			BigInt string `json:"#bigint"`
		} `json:"#set"`
	}
	if err := json.Unmarshal(raw, &encoded); err != nil {
		return nil, fmt.Errorf("decode ITF integer set %q: %w", name, err)
	}
	result := make(map[int64]struct{}, len(encoded.Values))
	for _, item := range encoded.Values {
		if item.BigInt == "" {
			return nil, fmt.Errorf("ITF set %q contains a non-integer value", name)
		}
		value, err := strconv.ParseInt(item.BigInt, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse ITF integer set %q: %w", name, err)
		}
		result[value] = struct{}{}
	}
	return result, nil
}
