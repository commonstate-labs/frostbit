package roast

import (
	"math/big"
	"testing"

	"dex/frost/core/curve"
	frostcore "dex/frost/core/frost"
	"dex/pb"

	"golang.org/x/crypto/sha3"
)

func TestBN128SigningPubkeyCanonicalizesAndNegatesShare(t *testing.T) {
	grp := curve.NewBN256Group()
	priv, groupPubkey := nonCanonicalBN128KeyForRoastTest(t, grp)
	canonicalPubkey, groupPubX, err := deriveSigningPubkeyForTask(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, groupPubkey, nil)
	if err != nil {
		t.Fatalf("deriveSigningPubkeyForTask err=%v", err)
	}
	if groupPubX.Cmp(new(big.Int).SetBytes(groupPubkey[:32])) != 0 {
		t.Fatal("groupPubX mismatch")
	}

	halfP := new(big.Int).Rsh(grp.Modulus(), 1)
	if new(big.Int).SetBytes(canonicalPubkey[32:]).Cmp(halfP) > 0 {
		t.Fatal("expected canonical BN128 pubkey Y")
	}

	rawShare := make([]byte, 32)
	priv.FillBytes(rawShare)
	share, negated, tweaked, err := deriveTaskSecretShare(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, groupPubkey, rawShare, nil)
	if err != nil {
		t.Fatalf("deriveTaskSecretShare err=%v", err)
	}
	if !negated {
		t.Fatal("expected non-canonical BN128 share to be negated")
	}
	if tweaked {
		t.Fatal("BN128 should not report tweak applied")
	}
	wantShare := new(big.Int).Neg(priv)
	wantShare.Mod(wantShare, grp.Order())
	if share.Cmp(wantShare) != 0 {
		t.Fatalf("share mismatch: got=%s want=%s", share, wantShare)
	}
}

func TestBN128CanonicalShareProducesVerifiableSignature(t *testing.T) {
	grp := curve.NewBN256Group()
	priv, groupPubkey := nonCanonicalBN128KeyForRoastTest(t, grp)
	rawShare := make([]byte, 32)
	priv.FillBytes(rawShare)

	canonicalPubkey, _, err := deriveSigningPubkeyForTask(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, groupPubkey, nil)
	if err != nil {
		t.Fatalf("deriveSigningPubkeyForTask err=%v", err)
	}
	share, _, _, err := deriveTaskSecretShare(pb.SignAlgo_SIGN_ALGO_SCHNORR_ALT_BN128, groupPubkey, rawShare, nil)
	if err != nil {
		t.Fatalf("deriveTaskSecretShare err=%v", err)
	}

	msg := hash32ForRoastTest([]byte("bn128 canonical signing"))
	nonce := big.NewInt(987654321)
	R := grp.ScalarBaseMult(nonce)
	e := bn128ChallengeForRoastTest(R.X, R.Y, new(big.Int).SetBytes(canonicalPubkey[:32]), new(big.Int).SetBytes(canonicalPubkey[32:]), msg, grp.Order())
	s := new(big.Int).Mul(e, share)
	s.Add(s, nonce)
	s.Mod(s, grp.Order())

	sig := make([]byte, 64)
	R.X.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	valid, err := frostcore.VerifyBN128(canonicalPubkey, msg, sig)
	if err != nil {
		t.Fatalf("VerifyBN128 err=%v", err)
	}
	if !valid {
		t.Fatal("expected canonical BN128 signature to verify")
	}
}

func nonCanonicalBN128KeyForRoastTest(t *testing.T, grp *curve.BN256Group) (*big.Int, []byte) {
	t.Helper()
	halfP := new(big.Int).Rsh(grp.Modulus(), 1)
	for i := int64(1); i < 10000; i++ {
		priv := big.NewInt(i)
		P := grp.ScalarBaseMult(priv)
		if P.Y.Cmp(halfP) > 0 {
			return priv, grp.SerializePoint(P)
		}
	}
	t.Fatal("failed to find non-canonical BN128 key")
	return nil, nil
}

func bn128ChallengeForRoastTest(Rx, Ry, Px, Py *big.Int, msg []byte, order *big.Int) *big.Int {
	h := sha3.NewLegacyKeccak256()
	writePadded := func(n *big.Int) {
		out := make([]byte, 32)
		n.FillBytes(out)
		h.Write(out)
	}
	writePadded(Rx)
	writePadded(Ry)
	writePadded(Px)
	writePadded(Py)
	h.Write(msg)
	e := new(big.Int).SetBytes(h.Sum(nil))
	return e.Mod(e, order)
}

func hash32ForRoastTest(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}
