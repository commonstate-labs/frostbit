package workers

import "fmt"

                                                                            
func (w *WithdrawWorker) buildSignedPackageBytes(signedPkg *SignedPackage) ([]byte, error) {
	if signedPkg == nil {
		return nil, fmt.Errorf("signed package is nil")
	}
	if len(signedPkg.Signature) > 0 {
		return append([]byte(nil), signedPkg.Signature...), nil
	}
	if len(signedPkg.Signatures) > 0 {
		total := 0
		for _, sig := range signedPkg.Signatures {
			total += len(sig)
		}
		combined := make([]byte, 0, total)
		for _, sig := range signedPkg.Signatures {
			combined = append(combined, sig...)
		}
		if len(combined) == 0 {
			return nil, fmt.Errorf("signed package signatures are empty")
		}
		return combined, nil
	}
	return nil, fmt.Errorf("signed package has no signature bytes")
}
