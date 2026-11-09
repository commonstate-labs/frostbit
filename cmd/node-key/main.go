                                                                                                         
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime"

	"dex/nodekey"
	"dex/utils"

	gethcrypto "github.com/ethereum/go-ethereum/crypto"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "node-key:", err)
		os.Exit(1)
	}
}

func passphrase(fd int, confirm bool) ([]byte, error) {
	if fd != -1 {
		return nodekey.ReadPassphraseFD(fd)
	}
	password, err := nodekey.ReadPassphraseTTY("Keystore passphrase: ")
	if err != nil {
		return nil, err
	}
	if !confirm {
		return password, nil
	}
	confirmation, err := nodekey.ReadPassphraseTTY("Confirm passphrase: ")
	if err != nil {
		nodekey.Wipe(password)
		return nil, err
	}
	defer nodekey.Wipe(confirmation)
	if !bytes.Equal(password, confirmation) {
		nodekey.Wipe(password)
		return nil, fmt.Errorf("passphrases do not match")
	}
	return password, nil
}

func dexAddress(raw []byte) (string, error) {
	key, err := gethcrypto.ToECDSA(raw)
	if err != nil {
		return "", fmt.Errorf("invalid secp256k1 private scalar")
	}
	defer func() {
		clear(key.D.Bits())
		key.D.SetInt64(0)
		runtime.KeepAlive(key)
	}()
	return utils.AddressFromSecp256k1PubKey(gethcrypto.CompressPubkey(&key.PublicKey))
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: node-key import --private-key-file <hex-file> --out <new-keystore> | inspect --keystore <file> | address --keystore <file>")
	}
	fs := flag.NewFlagSet("node-key "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	switch args[0] {
	case "import":
		source := fs.String("private-key-file", "", "existing private file containing one 64-character hex key")
		out := fs.String("out", "", "new encrypted Geth V3 file; never overwritten")
		passwordFD := fs.Int("password-fd", -1, "optional inherited passphrase FD >=3; otherwise prompt without echo; use a long random passphrase (12 bytes minimum)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *source == "" || *out == "" {
			return fmt.Errorf("import requires --private-key-file and --out, with no positional arguments")
		}
		if *passwordFD != -1 && *passwordFD < 3 {
			return fmt.Errorf("password FD must be >=3")
		}
		raw, err := nodekey.ReadPrivateKeyFile(*source)
		if err != nil {
			return err
		}
		defer nodekey.Wipe(raw)
		password, err := passphrase(*passwordFD, true)
		if err != nil {
			return err
		}
		defer nodekey.Wipe(password)
		address, err := dexAddress(raw)
		if err != nil {
			return err
		}
		if err := nodekey.EncryptFile(*out, raw, password); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Created encrypted keystore for %s. Original key file was not changed.\n", address)
		return err
	case "inspect":
		path := fs.String("keystore", "", "encrypted Geth V3 keystore to inspect without decrypting")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *path == "" {
			return fmt.Errorf("inspect requires --keystore, with no positional arguments")
		}
		metadata, err := nodekey.InspectFile(*path)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Version: %d\nCipher: %s\nKDF: %s (N=%d r=%d p=%d)\nEthereum metadata address: 0x%s (not a DEX address; use address to verify)\n", metadata.Version, metadata.Cipher, metadata.KDF, metadata.ScryptN, metadata.ScryptR, metadata.ScryptP, metadata.EthereumAddress)
		return err
	case "address":
		path := fs.String("keystore", "", "encrypted Geth V3 keystore")
		passwordFD := fs.Int("password-fd", -1, "optional inherited passphrase FD >=3; otherwise prompt without echo")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 0 || *path == "" {
			return fmt.Errorf("address requires --keystore, with no positional arguments")
		}
		if *passwordFD != -1 && *passwordFD < 3 {
			return fmt.Errorf("password FD must be >=3")
		}
		password, err := passphrase(*passwordFD, false)
		if err != nil {
			return err
		}
		defer nodekey.Wipe(password)
		raw, err := nodekey.ReadEncryptedFile(*path, password)
		if err != nil {
			return err
		}
		defer nodekey.Wipe(raw)
		address, err := dexAddress(raw)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, address)
		return err
	default:
		return fmt.Errorf("unknown command; use import, inspect or address")
	}
}
