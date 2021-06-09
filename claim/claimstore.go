package claim

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/pkg/errors"

	"github.com/cnabio/cnab-go/storage"
	"github.com/cnabio/cnab-go/utils/crud"
)

// ItemType is the location in the backing store where claims are persisted.
const (
	// Deprecated: ItemType has been replaced by ItemTypeClaims.
	ItemType = "claims"

	ItemTypeInstallations = "installations"
	ItemTypeClaims        = "claims"
	ItemTypeResults       = "results"
	ItemTypeOutputs       = "outputs"
)

var (
	// ErrInstallationNotFound represents an installation not found in claim storage
	ErrInstallationNotFound = errors.New("Installation does not exist")

	// ErrClaimNotFound represents a claim not found in claim storage
	ErrClaimNotFound = errors.New("Claim does not exist")

	// ErrResultNotFound represents a result not found in claim storage
	ErrResultNotFound = errors.New("Result does not exist")

	// ErrOutputNotFound represents an output not found in claim storage
	ErrOutputNotFound = errors.New("Output does not exist")
)

var _ Provider = &Store{}

// Store is a persistent store for claims.
type Store struct {
	lock         sync.Mutex
	backingStore crud.ManagedStore
	encrypt      storage.EncryptionHandler
	decrypt      storage.EncryptionHandler
}

// NewClaimStore creates a persistent store for claims using the specified
// backing datastore.
func NewClaimStore(store crud.ManagedStore, encrypt storage.EncryptionHandler, decrypt storage.EncryptionHandler) Store {
	if encrypt == nil {
		encrypt = storage.NoOpEncryptionHandler
	}

	if decrypt == nil {
		decrypt = storage.NoOpEncryptionHandler
	}

	return Store{
		backingStore: store,
		encrypt:      encrypt,
		decrypt:      decrypt,
	}
}

// NewClaimStoreFileExtensions builds a FileExtensions map suitable for use
// with a crud.FileSystem for a ClaimStore.
func NewClaimStoreFileExtensions() map[string]string {
	const json = ".json"
	return map[string]string{
		ItemTypeInstallations: json,
		ItemTypeClaims:        json,
		ItemTypeResults:       json,
		ItemTypeOutputs:       "",
	}
}

// GetBackingStore returns the data store behind this claim store.
func (s Store) GetBackingStore() crud.ManagedStore {
	return s.backingStore
}

func (s Store) ListInstallations(namespace string) ([]string, error) {
	if namespace == "" {
		namespace = storage.NamespaceGlobal
	}

	names, err := s.backingStore.List(ItemTypeInstallations, namespace)
	sort.Strings(names)
	return names, err
}

func (s Store) ListClaims(installation string) ([]string, error) {
	claims, err := s.backingStore.List(ItemTypeClaims, installation)
	// Depending on the underlying store, we either could not get
	// any claims, or an error, so handle either
	if len(claims) == 0 {
		return nil, ErrInstallationNotFound
	}
	sort.Strings(claims)
	return claims, s.handleNotExistsError(err, ErrInstallationNotFound)
}

func (s Store) ListResults(claimID string) ([]string, error) {
	results, err := s.backingStore.List(ItemTypeResults, claimID)
	if err != nil {
		// Gracefully handle a claim not having any results
		if strings.Contains(err.Error(), crud.ErrRecordDoesNotExist.Error()) {
			return nil, nil
		}
		return nil, err
	}

	sort.Strings(results)
	return results, nil
}

func (s Store) ListOutputs(resultID string) ([]string, error) {
	outputNames, err := s.backingStore.List(ItemTypeOutputs, resultID)
	if err != nil {
		// Gracefully handle a result not having any outputs
		if strings.Contains(err.Error(), crud.ErrRecordDoesNotExist.Error()) {
			return nil, nil
		}
		return nil, err
	}

	// outputs are keyed with the result, like RESULTID-OUTPUTNAME to make them unique
	// Strip off RESULTID- and return just OUTPUTNAME
	for i, fullName := range outputNames {
		outputNames[i] = strings.TrimPrefix(fullName, resultID+"-")
	}
	sort.Strings(outputNames)
	return outputNames, nil
}

func (s Store) ReadInstallation(namespace string, name string) (Installation, error) {
	bytes, err := s.backingStore.Read(ItemTypeInstallations, InstallationKey(namespace, name))
	if err != nil {
		return Installation{}, s.handleNotExistsError(err, ErrInstallationNotFound)
	}

	installation := Installation{}
	err = json.Unmarshal(bytes, &installation)
	return installation, err
}

func (s Store) ReadAllInstallations() ([]Installation, error) {
	items, err := s.backingStore.ReadAll(ItemTypeInstallations, "")
	if err != nil {
		return nil, err
	}

	installations := make(InstallationByName, len(items))
	for i, bytes := range items {
		var installation Installation
		err = json.Unmarshal(bytes, &installation)
		if err != nil {
			return nil, errors.Wrap(err, "error unmarshaling installation")
		}
		installations[i] = installation
	}

	sort.Sort(installations)
	return installations, nil
}

// DEPRECATED: Use Store.ReadInstallation instead, now that status is stored on the installation document.
func (s Store) ReadInstallationStatus(installation string) (Installation, error) {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return Installation{}, err
	}

	claimIds, err := s.ListClaims(installation)
	if err != nil {
		return Installation{}, err
	}

	var claims Claims
	if len(claimIds) > 0 {
		sort.Strings(claimIds)
		lastClaimID := claimIds[len(claimIds)-1]
		c, err := s.ReadClaim(lastClaimID)
		if err != nil {
			return Installation{}, err
		}

		resultIDs, err := s.ListResults(lastClaimID)
		if err != nil {
			return Installation{}, err
		}

		if len(resultIDs) > 0 {
			sort.Strings(resultIDs)
			lastResultID := resultIDs[len(resultIDs)-1]
			r, err := s.ReadResult(lastResultID)
			if err != nil {
				return Installation{}, err
			}
			c.results = &Results{r}
		}

		claims = append(claims, c)

		i := &Installation{Name: installation}
		i.LoadClaims(claims)
		return *i, nil
	}

	return Installation{}, ErrInstallationNotFound
}

func (s Store) ReadAllInstallationStatus() ([]Installation, error) {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return nil, err
	}

	names, err := s.ListInstallations("")
	if err != nil {
		return nil, err
	}

	installations := make([]Installation, 0, len(names))
	for _, name := range names {
		installation, err := s.ReadInstallationStatus(name)
		if err != nil {
			return nil, err
		}
		installations = append(installations, installation)
	}

	return installations, nil
}

func (s Store) ReadClaim(claimID string) (Claim, error) {
	bytes, err := s.backingStore.Read(ItemTypeClaims, claimID)
	if err != nil {
		return Claim{}, s.handleNotExistsError(err, ErrClaimNotFound)
	}

	bytes, err = s.decrypt(bytes)
	if err != nil {
		return Claim{}, errors.Wrapf(err, "error decrypting claim %s", claimID)
	}

	claim := Claim{}
	err = json.Unmarshal(bytes, &claim)
	return claim, err
}

func (s Store) ReadAllClaims(installation string) ([]Claim, error) {
	items, err := s.backingStore.ReadAll(ItemTypeClaims, installation)
	if err != nil {
		return nil, s.handleNotExistsError(err, ErrInstallationNotFound)
	}

	if len(items) == 0 {
		return nil, ErrInstallationNotFound
	}

	claims := make(Claims, len(items))
	for i, bytes := range items {
		bytes, err = s.decrypt(bytes)
		if err != nil {
			return nil, errors.Wrap(err, "error decrypting claim")
		}

		var claim Claim
		err = json.Unmarshal(bytes, &claim)
		if err != nil {
			return nil, errors.Wrap(err, "error unmarshaling claim")
		}
		claims[i] = claim
	}

	sort.Sort(claims)
	return claims, nil
}

func (s Store) ReadLastClaim(installation string) (Claim, error) {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return Claim{}, err
	}

	claimIds, err := s.backingStore.List(ItemTypeClaims, installation)
	if err != nil {
		return Claim{}, s.handleNotExistsError(err, ErrInstallationNotFound)
	}

	if len(claimIds) == 0 {
		return Claim{}, ErrInstallationNotFound
	}

	sort.Strings(claimIds)
	lastClaimID := claimIds[len(claimIds)-1]

	return s.ReadClaim(lastClaimID)
}

func (s Store) ReadResult(resultID string) (Result, error) {
	bytes, err := s.backingStore.Read(ItemTypeResults, resultID)
	if err != nil {
		return Result{}, s.handleNotExistsError(err, ErrResultNotFound)
	}
	result := Result{}
	err = json.Unmarshal(bytes, &result)
	return result, err
}

func (s Store) ReadAllResults(claimID string) ([]Result, error) {
	items, err := s.backingStore.ReadAll(ItemTypeResults, claimID)
	if err != nil {
		// Gracefully handle a claim not having any results
		if strings.Contains(err.Error(), crud.ErrRecordDoesNotExist.Error()) {
			return nil, nil
		}
		return nil, err
	}

	results := make(Results, len(items))
	for i, bytes := range items {
		var result Result
		err = json.Unmarshal(bytes, &result)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling result: %v", err)
		}
		results[i] = result
	}

	sort.Sort(results)
	return results, nil
}

// ReadLastOutputs returns the most recent (last) value of each output associated
// with the installation.
func (s Store) ReadLastOutputs(installation string) (Outputs, error) {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return Outputs{}, err
	}

	return s.readLastOutputs(installation, "")
}

// ReadLastOutput returns the most recent value (last) of the specified Output associated
// with the installation.
func (s Store) ReadLastOutput(installation string, name string) (Output, error) {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return Output{}, err
	}

	outputs, err := s.readLastOutputs(installation, name)
	if err != nil {
		return Output{}, err
	}

	if o, ok := outputs.GetByName(name); ok {
		return o, nil
	}

	return Output{}, ErrOutputNotFound
}

// readLastOutputs returns the most recent (last) value of the specified output,
// or all if none if no filter is specified, associated with the installation,
// sorted by name.
func (s Store) readLastOutputs(installation string, filterOutput string) (Outputs, error) {
	var results Results

	claims, err := s.ReadAllClaims(installation)
	if err != nil {
		return Outputs{}, err
	}

	for _, c := range claims {
		scopedClaim := c
		resultIds, err := s.ListResults(c.ID)
		if err != nil {
			return Outputs{}, err
		}
		for _, resultID := range resultIds {
			results = append(results, Result{
				ID:      resultID,
				ClaimID: c.ID,
				claim:   &scopedClaim,
			})
		}
	}

	// Determine the result that contains the final output value for each output
	// outputName -> resultID
	sort.Sort(results)
	lastOutputs := map[string]Result{}
	for _, result := range results {
		outputNames, err := s.ListOutputs(result.ID)
		if err != nil {
			return Outputs{}, err
		}
		for _, outputName := range outputNames {
			// Figure out if we want to retrieve and return this output
			if filterOutput == "" || filterOutput == outputName {
				lastOutputs[outputName] = result
			}
		}
	}

	outputs := make([]Output, 0, len(lastOutputs))
	for outputName, result := range lastOutputs {
		output, err := s.ReadOutput(*result.claim, result, outputName)
		if err != nil {
			return Outputs{}, err
		}

		outputs = append(outputs, output)
	}

	return NewOutputs(outputs), nil
}

func (s Store) ReadLastResult(claimID string) (Result, error) {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return Result{}, err
	}

	resultIDs, err := s.backingStore.List(ItemTypeResults, claimID)
	if err != nil {
		return Result{}, s.handleNotExistsError(err, ErrClaimNotFound)
	}

	if len(resultIDs) == 0 {
		return Result{}, fmt.Errorf("claim %s has no results", claimID)
	}

	sort.Strings(resultIDs)
	lastResultID := resultIDs[len(resultIDs)-1]

	return s.ReadResult(lastResultID)
}

func (s Store) ReadOutput(c Claim, r Result, outputName string) (Output, error) {
	bytes, err := s.backingStore.Read(ItemTypeOutputs, s.outputKey(r.ID, outputName))
	if err != nil {
		return Output{}, s.handleNotExistsError(err, ErrOutputNotFound)
	}

	sensitive, err := c.Bundle.IsOutputSensitive(outputName)
	if err != nil {
		sensitive = false // If it's not marked as sensitive, it was stored unencrypted
	}

	if sensitive {
		bytes, err = s.decrypt(bytes)
		if err != nil {
			return Output{}, errors.Wrapf(err, "error decrypting output %s", outputName)
		}
	}

	return NewOutput(c, r, outputName, bytes), nil
}

func (s Store) SaveInstallation(i Installation) error {
	return crud.SaveDocument(s.backingStore, i, s.encrypt)
}

func (s Store) SaveClaim(c Claim) error {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return err
	}

	err = crud.SaveDocument(s.backingStore, c, s.encrypt)
	if err != nil {
		return err
	}

	// Update the installation status when the action performed modifies installation resources
	// Ignore actions like "logs", or "status".
	if modifies, _ := c.IsModifyingAction(); modifies {
		s.lock.Lock()
		defer s.lock.Unlock()
		i, err := s.ReadInstallation("", c.Installation)
		if err != nil {
			return err
		}

		i = i.ApplyClaim(c)
		return s.SaveInstallation(i)
	}

	return nil
}

// SaveResult saves the specified Result and updates the status of the Installation.
func (s Store) SaveResult(r Result) error {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return err
	}

	err = crud.SaveDocument(s.backingStore, r, s.encrypt)
	if err != nil {
		return err
	}

	// Update the installation status when the action performed modifies installation resources
	// Ignore actions like "logs", or "status".
	if r.claim != nil {
		if modifies, _ := r.claim.IsModifyingAction(); modifies {
			s.lock.Lock()
			defer s.lock.Unlock()
			i, err := s.ReadInstallation("", r.claim.Installation)
			if err != nil {
				return err
			}

			i = i.ApplyResult(r)
			return s.SaveInstallation(i)
		}
	}

	return nil
}

func (s Store) SaveOutput(o Output) error {
	if o.claim.ID == "" {
		return errors.New("output.Claim is not set")
	}

	sensitive, err := o.claim.Bundle.IsOutputSensitive(o.Name)
	if err != nil {
		sensitive = false // If it's not marked as sensitive, it was stored unencrypted
	}

	data := o.Value
	if sensitive {
		data, err = s.encrypt(o.Value)
		if err != nil {
			return errors.Wrapf(err, "error encrypting output %s for result %s of installation %s", o.Name, o.result.ID, o.claim.Installation)
		}
	}

	return s.backingStore.Save(ItemTypeOutputs, o.GetGroup(), s.outputKey(o.result.ID, o.Name), data)
}

func (s Store) DeleteInstallation(installation string) error {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return err
	}

	claimIds, err := s.ListClaims(installation)
	if err != nil {
		return err
	}

	for _, claimID := range claimIds {
		err := s.DeleteClaim(claimID)
		if err != nil {
			return err
		}
	}

	err = s.backingStore.Delete(ItemTypeInstallations, installation)
	return s.handleNotExistsError(err, ErrInstallationNotFound)
}

func (s Store) DeleteClaim(claimID string) error {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return err
	}

	resultIds, err := s.ListResults(claimID)
	if err != nil {
		return err
	}

	for _, resultID := range resultIds {
		err := s.DeleteResult(resultID)
		if err != nil {
			return err
		}
	}

	err = s.backingStore.Delete(ItemTypeClaims, claimID)
	return s.handleNotExistsError(err, ErrClaimNotFound)
}

func (s Store) DeleteResult(resultID string) error {
	handleClose, err := s.backingStore.HandleConnect()
	defer handleClose()
	if err != nil {
		return err
	}

	outputNames, err := s.ListOutputs(resultID)
	if err != nil {
		return err
	}

	for _, output := range outputNames {
		err := s.DeleteOutput(resultID, output)
		if err != nil {
			return err
		}
	}

	err = s.backingStore.Delete(ItemTypeResults, resultID)
	return s.handleNotExistsError(err, ErrResultNotFound)
}

func (s Store) DeleteOutput(resultID string, outputName string) error {
	err := s.backingStore.Delete(ItemTypeOutputs, s.outputKey(resultID, outputName))
	return s.handleNotExistsError(err, ErrOutputNotFound)
}

// outputKey returns the full name of an Output suitable for storage.
// ResultId is used to create a unique name because output names are
// not unique across bundle executions.
func (s Store) outputKey(resultID string, output string) string {
	return resultID + "-" + output
}

// handleNotExistsError converts generic ErrRecordDoesNotExist errors from the crud layer
// into the specified typed error, if present.
func (s Store) handleNotExistsError(crudError error, typedError error) error {
	if crudError != nil && strings.Contains(crudError.Error(), crud.ErrRecordDoesNotExist.Error()) {
		return typedError
	}
	return crudError
}
