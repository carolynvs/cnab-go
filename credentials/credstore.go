package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cnabio/cnab-go/utils/crud"
)

// ItemType is the location in the backing store where credentials are persisted.
const ItemType = "credentials"

// ErrNotFound represents a credential set not found in storage
var ErrNotFound = errors.New("Credential set does not exist")

// Store is a persistent store for credential sets.
type Store struct {
	backingStore *crud.BackingStore
}

// NewCredentialStore creates a persistent store for credential sets using the specified
// backing key-blob store.
func NewCredentialStore(store *crud.BackingStore) Store {
	return Store{
		backingStore: store,
	}
}

// GetBackingStore returns the data store behind this credentials store.
func (s Store) GetBackingStore() *crud.BackingStore {
	return s.backingStore
}

// List lists the names of the stored credential sets.
func (s Store) List() ([]string, error) {
	return s.backingStore.List(ItemType, "")
}

// Save a credential set. Any previous version of the credential set is overwritten.
func (s Store) Save(cred CredentialSet) error {
	bytes, err := json.MarshalIndent(cred, "", "  ")
	if err != nil {
		return err
	}
	return s.backingStore.Save(ItemType, "", cred.Name, bytes)
}

// Read loads the credential set with the given name from the store.
func (s Store) Read(name string) (CredentialSet, error) {
	bytes, err := s.backingStore.Read(ItemType, name)
	if err != nil {
		if strings.Contains(err.Error(), crud.ErrRecordDoesNotExist.Error()) {
			return CredentialSet{}, ErrNotFound
		}
		return CredentialSet{}, err
	}
	credset := CredentialSet{}
	err = json.Unmarshal(bytes, &credset)
	return credset, err
}

// ReadAll retrieves all the credential sets.
func (s Store) ReadAll() ([]CredentialSet, error) {
	results, err := s.backingStore.ReadAll(ItemType, "")
	if err != nil {
		return nil, err
	}

	creds := make([]CredentialSet, len(results))
	for i, bytes := range results {
		var cs CredentialSet
		err = json.Unmarshal(bytes, &cs)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling credential set: %v", err)
		}
		creds[i] = cs
	}

	return creds, nil
}

// Delete deletes a credential set from the store.
func (s Store) Delete(name string) error {
	return s.backingStore.Delete(ItemType, name)
}
