                                             
  
                                                                             
                                                                          
                                                                            
                                                                               
                             
package adapters

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	iface "dex/interfaces"
	"dex/keys"
)

const (
	shareWrapKeyLen = 32           
	shareNonceLen   = 12                
	shareTagLen     = 16           

	shareBlobMagic          = "DEXFRSHR"
	shareBlobVersion        = byte(1)
	shareBlobHeaderLen      = len(shareBlobMagic) + 1
	maxShareStorageKeyLen   = 4096
	canonicalShareAADDomain = "DEX/FROST/NODE-LOCAL/CANONICAL-SHARE/V1"
	pendingShareAADDomain   = "DEX/FROST/NODE-LOCAL/PENDING-SHARE/V1"
	revealAADDomain         = "DEX/FROST/NODE-LOCAL/DKG-REVEAL/V1"

	externalWrapMarkerVersion = byte(1)
	externalWrapMarkerLen     = 1 + sha256.Size
)

                                                                          
                                                                              
                                                           
type shareCipher struct {
	dbManager iface.NodeLocalWriter

	mu              sync.Mutex
	gcm             cipher.AEAD
	init            bool
	external        bool
	externalWrapKey []byte
}

func newShareCipher(dbManager iface.NodeLocalWriter) *shareCipher {
	return &shareCipher{dbManager: dbManager}
}

func newShareCipherWithExternalWrapKey(dbManager iface.NodeLocalWriter, wrapKey []byte) (*shareCipher, error) {
	if len(wrapKey) != shareWrapKeyLen {
		return nil, fmt.Errorf("external FROST wrap key must be %d bytes", shareWrapKeyLen)
	}
	c := &shareCipher{
		dbManager:       dbManager,
		external:        true,
		externalWrapKey: append([]byte(nil), wrapKey...),
	}
	if _, err := c.ensure(); err != nil {
		clear(c.externalWrapKey)
		c.externalWrapKey = nil
		return nil, err
	}
	return c, nil
}

                                                                              
                                                              
func (c *shareCipher) ensure() (cipher.AEAD, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.init && c.gcm != nil {
		return c.gcm, nil
	}
	if c.dbManager == nil {
		return nil, errors.New("share cipher: db manager not available")
	}

	var (
		key []byte
		err error
	)
	if c.external {
		key = append([]byte(nil), c.externalWrapKey...)
		if len(key) != shareWrapKeyLen {
			return nil, errors.New("share cipher: external wrap key unavailable")
		}
	} else {
		key, err = c.loadOrCreateLegacyWrapKey()
		if err != nil {
			return nil, err
		}
	}
	defer clear(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	if gcm.NonceSize() != shareNonceLen {
		return nil, fmt.Errorf("unexpected nonce size %d", gcm.NonceSize())
	}

	if c.external {
		if err := c.activateExternalWrapKey(gcm, key); err != nil {
			return nil, err
		}
		clear(c.externalWrapKey)
		c.externalWrapKey = nil
	} else if err := c.migrateEncryptedLocalRecords(gcm); err != nil {
		return nil, err
	}

	c.gcm = gcm
	c.init = true
	return c.gcm, nil
}

                                                                            
                                                                          
                                                            
func (c *shareCipher) loadOrCreateLegacyWrapKey() ([]byte, error) {
	if _, err := c.dbManager.Get(keys.KeyFrostLocalShareWrapExternalMarker()); err == nil {
		return nil, errors.New("external FROST wrap key is required")
	} else if !isLocalShareNotFound(err) {
		return nil, fmt.Errorf("read external wrap marker: %w", err)
	}

	k := keys.KeyFrostLocalShareWrapKey()
	existing, err := c.dbManager.Get(k)
	if err == nil {
		if len(existing) != shareWrapKeyLen {
			clear(existing)
			return nil, fmt.Errorf("persisted FROST wrap key has invalid length %d", len(existing))
		}
		out := append([]byte(nil), existing...)
		clear(existing)
		return out, nil
	}
	if !isLocalShareNotFound(err) {
		return nil, fmt.Errorf("read wrap key: %w", err)
	}

	records, err := c.encryptedLocalRecords()
	if err != nil {
		return nil, err
	}
	defer clearEncryptedLocalRecords(records)
	if len(records) != 0 {
		return nil, errors.New("FROST wrap key is missing while encrypted local records exist")
	}

	fresh := make([]byte, shareWrapKeyLen)
	if _, err := rand.Read(fresh); err != nil {
		return nil, fmt.Errorf("gen wrap key: %w", err)
	}
	c.dbManager.EnqueueSet(k, string(fresh))
	if err := c.dbManager.ForceFlush(); err != nil {
		clear(fresh)
		return nil, fmt.Errorf("persist wrap key: %w", err)
	}
	return fresh, nil
}

                                                                             
                                                                     
                                                                             
                                                                              
func (c *shareCipher) activateExternalWrapKey(gcm cipher.AEAD, supplied []byte) error {
	markerKey := keys.KeyFrostLocalShareWrapExternalMarker()
	marker, markerErr := c.dbManager.Get(markerKey)
	markerPresent := markerErr == nil
	if markerErr != nil && !isLocalShareNotFound(markerErr) {
		return fmt.Errorf("read external wrap marker: %w", markerErr)
	}
	if markerPresent {
		if err := validateExternalWrapMarker(marker, supplied); err != nil {
			return err
		}
	}

	legacyKeyName := keys.KeyFrostLocalShareWrapKey()
	legacyKey, legacyErr := c.dbManager.Get(legacyKeyName)
	legacyPresent := legacyErr == nil
	if legacyErr != nil && !isLocalShareNotFound(legacyErr) {
		return fmt.Errorf("read legacy wrap key: %w", legacyErr)
	}
	if legacyPresent {
		defer clear(legacyKey)
		if len(legacyKey) != shareWrapKeyLen {
			return fmt.Errorf("persisted FROST wrap key has invalid length %d", len(legacyKey))
		}
		if subtle.ConstantTimeCompare(legacyKey, supplied) != 1 {
			return errors.New("external FROST wrap key does not match persisted legacy key")
		}
	}

	records, err := c.encryptedLocalRecords()
	if err != nil {
		return err
	}
	defer clearEncryptedLocalRecords(records)
	if !markerPresent && !legacyPresent && len(records) != 0 {
		return errors.New("FROST wrap key is missing while encrypted local records exist")
	}
	if err := c.migrateEncryptedLocalRecordSet(gcm, records); err != nil {
		return err
	}

	if !markerPresent {
		c.dbManager.EnqueueSet(markerKey, string(makeExternalWrapMarker(supplied)))
		if err := c.dbManager.ForceFlush(); err != nil {
			return fmt.Errorf("persist external wrap marker: %w", err)
		}
	}

	if legacyPresent {
		c.dbManager.EnqueueDel(legacyKeyName)
		if err := c.dbManager.ForceFlush(); err != nil {
			return fmt.Errorf("delete persisted legacy wrap key: %w", err)
		}
	}
	return nil
}

type encryptedLocalRecord struct {
	key  string
	blob []byte
}

func clearEncryptedLocalRecords(records []encryptedLocalRecord) {
	for i := range records {
		clear(records[i].blob)
		records[i].blob = nil
	}
}

func (c *shareCipher) encryptedLocalRecords() ([]encryptedLocalRecord, error) {
	prefixes := []string{
		keys.KeyFrostLocalSharePrefix(),
		keys.KeyFrostPendingLocalSharePrefix(),
		keys.KeyFrostLocalDkgRevealPrefix(),
	}
	var records []encryptedLocalRecord
	for _, prefix := range prefixes {
		items, err := c.dbManager.Scan(prefix)
		if err != nil {
			return nil, fmt.Errorf("scan encrypted FROST local records: %w", err)
		}
		for key, blob := range items {
			records = append(records, encryptedLocalRecord{
				key:  key,
				blob: append([]byte(nil), blob...),
			})
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].key < records[j].key })
	return records, nil
}

                                                                           
                                                                                
                                                                              
                                                                               
                                                                 
func (c *shareCipher) migrateEncryptedLocalRecords(gcm cipher.AEAD) error {
	records, err := c.encryptedLocalRecords()
	if err != nil {
		return err
	}
	defer clearEncryptedLocalRecords(records)
	return c.migrateEncryptedLocalRecordSet(gcm, records)
}

func (c *shareCipher) migrateEncryptedLocalRecordSet(gcm cipher.AEAD, records []encryptedLocalRecord) error {
	type replacement struct {
		key  string
		blob []byte
	}
	replacements := make([]replacement, 0)
	defer func() {
		for i := range replacements {
			clear(replacements[i].blob)
			replacements[i].blob = nil
		}
	}()

	for _, record := range records {
		plain, legacy, err := openShareBlobCompat(gcm, record.key, record.blob)
		if err != nil {
			return fmt.Errorf("validate encrypted FROST local record %q: %w", record.key, err)
		}
		if legacy {
			blob, sealErr := sealShareBlob(gcm, record.key, plain)
			if sealErr != nil {
				clear(plain)
				return fmt.Errorf("migrate encrypted FROST local record %q: %w", record.key, sealErr)
			}
			replacements = append(replacements, replacement{key: record.key, blob: blob})
		}
		clear(plain)
	}

	if len(replacements) == 0 {
		return nil
	}
	for _, replacement := range replacements {
		c.dbManager.EnqueueSet(replacement.key, string(replacement.blob))
	}
	if err := c.dbManager.ForceFlush(); err != nil {
		return fmt.Errorf("persist versioned FROST local records: %w", err)
	}
	return nil
}

func makeExternalWrapMarker(key []byte) []byte {
	fingerprint := sha256.Sum256(key)
	marker := make([]byte, externalWrapMarkerLen)
	marker[0] = externalWrapMarkerVersion
	copy(marker[1:], fingerprint[:])
	return marker
}

func validateExternalWrapMarker(marker, supplied []byte) error {
	if len(marker) != externalWrapMarkerLen || marker[0] != externalWrapMarkerVersion {
		return errors.New("external FROST wrap marker is invalid")
	}
	expected := makeExternalWrapMarker(supplied)
	defer clear(expected)
	if subtle.ConstantTimeCompare(marker, expected) != 1 {
		return errors.New("external FROST wrap key does not match persisted marker")
	}
	return nil
}

                                                                             
                                                                             
                                                                                
                    
func (c *shareCipher) encrypt(storageKey string, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("empty plaintext")
	}
	gcm, err := c.ensure()
	if err != nil {
		return nil, err
	}
	return sealShareBlob(gcm, storageKey, plaintext)
}

                                                                       
                                                                             
func (c *shareCipher) decrypt(storageKey string, blob []byte) ([]byte, error) {
	gcm, err := c.ensure()
	if err != nil {
		return nil, err
	}
	return openShareBlob(gcm, storageKey, blob)
}

func sealShareBlob(gcm cipher.AEAD, storageKey string, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, errors.New("empty plaintext")
	}
	aad, err := shareBlobAAD(storageKey)
	if err != nil {
		return nil, err
	}
	defer clear(aad)

	nonce := make([]byte, shareNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("gen nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plaintext, aad)
	out := make([]byte, 0, shareBlobHeaderLen+shareNonceLen+len(sealed))
	out = append(out, shareBlobMagic...)
	out = append(out, shareBlobVersion)
	out = append(out, nonce...)
	out = append(out, sealed...)
	clear(sealed)
	return out, nil
}

func openShareBlob(gcm cipher.AEAD, storageKey string, blob []byte) ([]byte, error) {
	if len(blob) < shareBlobHeaderLen || !bytes.Equal(blob[:len(shareBlobMagic)], []byte(shareBlobMagic)) {
		return nil, errors.New("legacy or unrecognized cipher blob")
	}
	if blob[len(shareBlobMagic)] != shareBlobVersion {
		return nil, fmt.Errorf("unsupported cipher blob version %d", blob[len(shareBlobMagic)])
	}
	if len(blob) < shareBlobHeaderLen+shareNonceLen+shareTagLen {
		return nil, errors.New("cipher blob too short")
	}
	aad, err := shareBlobAAD(storageKey)
	if err != nil {
		return nil, err
	}
	defer clear(aad)
	nonce := blob[shareBlobHeaderLen : shareBlobHeaderLen+shareNonceLen]
	sealed := blob[shareBlobHeaderLen+shareNonceLen:]
	plain, err := gcm.Open(nil, nonce, sealed, aad)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}
	return plain, nil
}

func openShareBlobCompat(gcm cipher.AEAD, storageKey string, blob []byte) ([]byte, bool, error) {
	if len(blob) >= len(shareBlobMagic) && bytes.Equal(blob[:len(shareBlobMagic)], []byte(shareBlobMagic)) {
		plain, err := openShareBlob(gcm, storageKey, blob)
		return plain, false, err
	}
	plain, err := openLegacyShareBlob(gcm, blob)
	return plain, true, err
}

func openLegacyShareBlob(gcm cipher.AEAD, blob []byte) ([]byte, error) {
	if len(blob) < shareNonceLen+shareTagLen {
		return nil, errors.New("cipher blob too short")
	}
	nonce := blob[:shareNonceLen]
	sealed := blob[shareNonceLen:]
	plain, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm open: %w", err)
	}
	return plain, nil
}

func shareBlobAAD(storageKey string) ([]byte, error) {
	if storageKey == "" || len(storageKey) > maxShareStorageKeyLen {
		return nil, errors.New("invalid FROST local secret storage key")
	}
	domain := ""
	switch {
	case strings.HasPrefix(storageKey, keys.KeyFrostLocalSharePrefix()) && len(storageKey) > len(keys.KeyFrostLocalSharePrefix()):
		domain = canonicalShareAADDomain
	case strings.HasPrefix(storageKey, keys.KeyFrostPendingLocalSharePrefix()) && len(storageKey) > len(keys.KeyFrostPendingLocalSharePrefix()):
		domain = pendingShareAADDomain
	case strings.HasPrefix(storageKey, keys.KeyFrostLocalDkgRevealPrefix()) && len(storageKey) > len(keys.KeyFrostLocalDkgRevealPrefix()):
		domain = revealAADDomain
	default:
		return nil, errors.New("unsupported FROST local secret storage key")
	}
	aad := make([]byte, 0, len(domain)+1+len(storageKey))
	aad = append(aad, domain...)
	aad = append(aad, 0)
	aad = append(aad, storageKey...)
	return aad, nil
}
