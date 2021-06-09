package storage

type Document interface {
	// GetType returns type of document with respect to where it should be stored.
	GetType() string

	// GetNamespace returns the namespace to which the document is scoped.
	GetNamespace() string

	// GetName returns the document name.
	// Name is only unique when combined with Namespace.
	GetName() string

	// GetData returns the storage representation of the document.
	GetData() ([]byte, error)

	// ShouldEncrypt determines if the document should be stored encrypted.
	ShouldEncrypt() bool
}

// EncryptionHandler is a function that transforms data by encrypting or decrypting it.
type EncryptionHandler func([]byte) ([]byte, error)

// NoOpEncryptHandler leaves the data unchanged.
var NoOpEncryptionHandler = func(data []byte) ([]byte, error) {
	return data, nil
}
