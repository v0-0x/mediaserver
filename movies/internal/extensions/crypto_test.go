package extensions

import (
	"testing"

	"github.com/dop251/goja"
)

// AES results honour the encoder passed to toString(), as in CryptoJS:
// decrypting to Utf8 gives back the plain text, not its base64.
func TestAESToStringEncoder(t *testing.T) {
	vm := goja.New()
	installCrypto(vm)
	v, err := vm.RunString(`
		var key = CryptoJS.enc.Utf8.parse('0123456789abcdef');
		var iv = CryptoJS.enc.Utf8.parse('fedcba9876543210');
		var enc = CryptoJS.AES.encrypt('hello world', key, { iv: iv });
		var dec = CryptoJS.AES.decrypt(enc.toString(), key, { iv: iv });
		[dec.toString(CryptoJS.enc.Utf8), dec.toString(CryptoJS.enc.Hex), enc.toString() === enc.toString(CryptoJS.enc.Base64)].join('|');
	`)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := v.String(), "hello world|68656c6c6f20776f726c64|true"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
