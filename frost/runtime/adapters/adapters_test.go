package adapters

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

                                                              
func TestFakeStateReaderAdapter(t *testing.T) {
	reader := NewFakeStateReader()

	                  
	data, exists, err := reader.Get("nonexistent")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, data)

	            
	reader.Set("key1", []byte("value1"))
	reader.Set("key2", []byte("value2"))
	reader.Set("prefix_a", []byte("a"))
	reader.Set("prefix_b", []byte("b"))

	             
	data, exists, err = reader.Get("key1")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, []byte("value1"), data)

	              
	var scanned []string
	err = reader.Scan("prefix_", func(k string, v []byte) bool {
		scanned = append(scanned, k)
		return true
	})
	assert.NoError(t, err)
	assert.Len(t, scanned, 2)
}

                                                              
func TestFakeTxSubmitterAdapter(t *testing.T) {
	submitter := NewFakeTxSubmitter()

	               
	txID, err := submitter.Submit("tx1")
	assert.NoError(t, err)
	assert.NotEmpty(t, txID)

	txID2, err := submitter.Submit("tx2")
	assert.NoError(t, err)
	assert.NotEmpty(t, txID2)

	                     
	assert.Equal(t, 2, submitter.SubmitCount())

	                        
	submitted := submitter.GetSubmitted()
	assert.Len(t, submitted, 2)
	assert.Equal(t, "tx1", submitted[0])
	assert.Equal(t, "tx2", submitted[1])

	                     
	submitter.SetShouldFail(true, errors.New("test error"))
	_, err = submitter.Submit("tx3")
	assert.Error(t, err)
	assert.Equal(t, "test error", err.Error())

	               
	submitter.Reset()
	assert.Equal(t, 0, submitter.SubmitCount())
	assert.Len(t, submitter.GetSubmitted(), 0)

	                              
	txID3, err := submitter.Submit("tx4")
	assert.NoError(t, err)
	assert.NotEmpty(t, txID3)
}

                                                          
func TestStateDBReaderAdapter(t *testing.T) {
	                 
	fakeDB := &fakeDBGetter{data: make(map[string][]byte)}
	fakeDB.data["key1"] = []byte("value1")
	fakeDB.data["key2"] = []byte("value2")

	reader := NewStateDBReader(fakeDB)

	                           
	data, exists, err := reader.Get("key1")
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, []byte("value1"), data)

	                              
	data, exists, err = reader.Get("nonexistent")
	assert.NoError(t, err)
	assert.False(t, exists)
	assert.Nil(t, data)

	              
	fakeDB.data["prefix_a"] = []byte("a")
	fakeDB.data["prefix_b"] = []byte("b")

	var scanned []string
	err = reader.Scan("prefix_", func(k string, v []byte) bool {
		scanned = append(scanned, k)
		return true
	})
	assert.NoError(t, err)
	assert.Len(t, scanned, 2)

	                 
	batch, err := reader.GetMany([]string{"key1", "key2", "missing"})
	assert.NoError(t, err)
	assert.Len(t, batch, 2)
	assert.Equal(t, []byte("value1"), batch["key1"])
	assert.Equal(t, []byte("value2"), batch["key2"])
}

                              
type fakeDBGetter struct {
	data map[string][]byte
}

func (f *fakeDBGetter) Get(key string) ([]byte, error) {
	if v, ok := f.data[key]; ok {
		return v, nil
	}
	return nil, errors.New("key not found")
}

func (f *fakeDBGetter) Scan(prefix string) (map[string][]byte, error) {
	result := make(map[string][]byte)
	for k, v := range f.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			result[k] = v
		}
	}
	return result, nil
}
