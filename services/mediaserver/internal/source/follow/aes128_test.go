package follow

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestAKeyThatIsNotTheOneThatSealedItIsCaught(t *testing.T) {
	secret, iv := bytes.Repeat([]byte{7}, 16), bytes.Repeat([]byte{9}, 16)
	block, _ := aes.NewCipher(secret)
	sealed := make([]byte, 32)
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(sealed, append([]byte("sixteen bytes!!!"), bytes.Repeat([]byte{16}, 16)...))
	if _, err := decryptAES128(sealed, bytes.Repeat([]byte{8}, 16), fmt.Sprintf("0x%x", iv)); err == nil {
		t.Error("decrypted with the wrong key as if it were right")
	}
	if clear, err := decryptAES128(sealed, secret, fmt.Sprintf("0x%x", iv)); err != nil || string(clear) != "sixteen bytes!!!" {
		t.Errorf("decrypted = %q (%v), want the sealed text", clear, err)
	}
}

func TestAnIVWrittenAsAShortHexIntegerDecrypts(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 16)
	iv := make([]byte, aes.BlockSize)
	iv[15] = 1
	block, _ := aes.NewCipher(secret)
	padded := append([]byte("hello"), bytes.Repeat([]byte{11}, 11)...)
	sealed := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(sealed, padded)
	for _, written := range []string{"0x1", "0X01", "0x00000000000000000000000000000001"} {
		if clear, err := decryptAES128(sealed, secret, written); err != nil || string(clear) != "hello" {
			t.Errorf("decrypt with %s = %q (%v), want hello", written, clear, err)
		}
	}
}

func TestAnOversizedKeyResponseIsRejectedWithoutReadingItsRemainder(t *testing.T) {
	unread := errors.New("read beyond the key size limit")
	body := io.MultiReader(strings.NewReader(strings.Repeat("x", 17)), iotest.ErrReader(unread))
	key, err := readAESKey(body)
	if err == nil || errors.Is(err, unread) || key != nil {
		t.Fatalf("readAESKey = %q, %v; want an invalid key without reading the rest of the response", key, err)
	}
}

func TestAnInvalidKeyIsNotAcceptedAsAnAES128Key(t *testing.T) {
	for _, size := range []int{0, 15, 16, 17} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			want := bytes.Repeat([]byte{7}, size)
			got, err := readAESKey(bytes.NewReader(want))
			if size == 16 {
				if err != nil || !bytes.Equal(got, want) {
					t.Fatalf("key = %q, %v", got, err)
				}
			} else if err == nil {
				t.Fatalf("accepted a %d-byte key", size)
			}
		})
	}
}
