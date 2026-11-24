package hap

import (
	"bufio"
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/devices/airplay/rtsp"
	"github.com/qlqq/roomusic/backend/internal/devices/lan"
	"golang.org/x/crypto/chacha20poly1305"
)

func pairingReceiver(t *testing.T, handler http.HandlerFunc) *rtsp.Client {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range addrs {
		ip, err := netip.ParsePrefix(a.String())
		if err != nil || !lan.IsLANAddress(ip.Addr()) {
			continue
		}
		listener, err := net.Listen("tcp4", netip.AddrPortFrom(ip.Addr(), 0).String())
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(handler)
		server.Listener.Close()
		server.Listener = listener
		server.Start()
		t.Cleanup(server.Close)
		client, err := rtsp.Dial(t.Context(), listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { client.Close() })
		return client
	}
	t.Skip("配对往返需要本机局域网 IPv4 地址")
	return nil
}

func pairingRequest(t *testing.T, w http.ResponseWriter, r *http.Request, hkp string) message {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err == nil && r.Method == "POST" && r.Header.Get("X-Apple-HKP") == hkp {
		var m message
		m, err = decode(body)
		if err == nil {
			return m
		}
	}
	t.Errorf("配对请求 %s %s，HKP = %q，错误 %v", r.Method, r.URL.Path, r.Header.Get("X-Apple-HKP"), err)
	w.WriteHeader(http.StatusBadRequest)
	return nil
}

func pairingResponse(w http.ResponseWriter, state byte, fields ...field) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(encode(append([]field{{tagState, []byte{state}}}, fields...)...))
}

func TestAirPlayPairSetupStateMachine(t *testing.T) {
	for _, tc := range []struct {
		name, pin, hkp      string
		transient, badProof bool
	}{
		{"transient", "3939", "4", true, false},
		{"transient bad receiver proof", "3939", "4", true, true},
		{"PIN", "0123", "3", false, false},
		{"PIN wrong code", "0123", "3", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			salt := hexBytes(rfc5054Salt)[1:]
			srpPeer := newAccessorySRP(primeHex, 5, sha512.New, "Pair-Setup", tc.pin, salt)
			accessory := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x52}, 32))
			id := []byte("fixture-accessory")
			identities := make(chan message, 1)
			var secret []byte
			wantStep := byte(1)
			client := pairingReceiver(t, func(w http.ResponseWriter, r *http.Request) {
				m := pairingRequest(t, w, r, tc.hkp)
				if m == nil {
					return
				}
				if r.URL.Path == "/pair-pin-start" && !tc.transient && wantStep == 1 {
					wantStep = 2
					return
				}
				if r.URL.Path != "/pair-setup" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				switch state := string(m[tagState]); {
				case state == "\x01" && (wantStep == 2 || tc.transient && wantStep == 1):
					if !bytes.Equal(m[tagMethod], []byte{0}) ||
						tc.transient && !bytes.Equal(m[tagFlags], []byte{0x10}) ||
						!tc.transient && len(m[tagFlags]) != 0 {
						t.Error("M1 配对方法或 transient 标志错误")
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					wantStep = 3
					pairingResponse(w, 2, field{tagSalt, salt}, field{tagPublicKey, srpPeer.public.Bytes()})
				case state == "\x03" && wantStep == 3:
					key, proof := srpPeer.hapProof(salt, m[tagPublicKey])
					secret = key
					if !bytes.Equal(proof, m[tagProof]) {
						pairingResponse(w, 4, field{tagError, []byte{2}})
						return
					}
					reply := digest(sha512.New, m[tagPublicKey], proof, key)
					if tc.badProof {
						reply[0] ^= 1
					}
					wantStep = 5
					pairingResponse(w, 4, field{tagProof, reply})
				case state == "\x05" && wantStep == 5 && !tc.transient:
					session := Session{secret: secret}
					aead, _ := session.pairingCipher("Pair-Setup-Encrypt")
					controller, err := openMessage(aead, "PS-Msg05", m[tagEncrypted])
					prefix, _ := session.pairingKey("Pair-Setup-Controller-Sign")
					pub := controller[tagPublicKey]
					if err != nil || len(pub) != ed25519.PublicKeySize ||
						!ed25519.Verify(pub, joined(prefix, controller[tagIdentifier], pub), controller[tagSignature]) {
						t.Errorf("M5 控制端身份签名无效，错误 %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					identities <- controller
					pub = accessory.Public().(ed25519.PublicKey)
					prefix, _ = session.pairingKey("Pair-Setup-Accessory-Sign")
					signed := encode(field{tagIdentifier, id}, field{tagPublicKey, pub},
						field{tagSignature, ed25519.Sign(accessory, joined(prefix, id, pub))})
					wantStep = 7
					pairingResponse(w, 6, field{tagEncrypted, aead.Seal(nil, nonce("PS-Msg06"), signed, nil)})
				default:
					t.Errorf("意外的 HAP 状态 %x，预期步骤 %d", state, wantStep)
					w.WriteHeader(http.StatusBadRequest)
				}
			})
			if tc.transient {
				session, err := PairTransient(t.Context(), client)
				if tc.badProof {
					if err == nil || !strings.Contains(err.Error(), "接收端证明无效") {
						t.Fatalf("错误 M4 证明得到 %v", err)
					}
				} else if err != nil || len(session.AudioKey()) != 32 {
					t.Fatalf("transient 配对错误 %v", err)
				}
				return
			}
			setup, err := BeginPIN(t.Context(), client)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := setup.Finish(t.Context(), "x123"); err == nil {
				t.Fatal("接受了非数字 PIN")
			}
			pin := tc.pin
			if tc.badProof {
				pin = "9999"
			}
			credentials, err := setup.Finish(t.Context(), pin)
			if tc.badProof {
				if err == nil || !strings.Contains(err.Error(), "接收端错误码 2") {
					t.Fatalf("错误 PIN 得到 %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				identity := <-identities
				if credentials.AccessoryID != string(id) ||
					!bytes.Equal(credentials.AccessoryPublicKey, accessory.Public().(ed25519.PublicKey)) ||
					credentials.ControllerID != string(identity[tagIdentifier]) ||
					!bytes.Equal(ed25519.NewKeyFromSeed(credentials.ControllerSeed).Public().(ed25519.PublicKey), identity[tagPublicKey]) {
					t.Fatal("PIN 凭据与交换身份不一致")
				}
			}
			if _, err := setup.Finish(t.Context(), tc.pin); err == nil || !strings.Contains(err.Error(), "挑战已使用") {
				t.Fatalf("重复使用挑战得到 %v", err)
			}
		})
	}
}

func TestAirPlayPairVerifyAndEncryptedControl(t *testing.T) {
	for _, wrongIdentity := range []bool{false, true} {
		t.Run(fmt.Sprintf("wrong_identity=%t", wrongIdentity), func(t *testing.T) {
			accessory := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x53}, 32))
			credentials := Credentials{
				ControllerSeed: bytes.Repeat([]byte{0x54}, 32), ControllerID: "fixture-controller",
				AccessoryID: "fixture-accessory", AccessoryPublicKey: accessory.Public().(ed25519.PublicKey),
			}
			accessoryKey, err := ecdh.X25519().NewPrivateKey(bytes.Repeat([]byte{0x55}, 32))
			if err != nil {
				t.Fatal(err)
			}
			peer := accessoryKey.PublicKey().Bytes()
			var local []byte
			var session Session
			serverDone := make(chan error, 1)
			client := pairingReceiver(t, func(w http.ResponseWriter, r *http.Request) {
				m := pairingRequest(t, w, r, "3")
				if m == nil {
					return
				}
				switch string(m[tagState]) {
				case "\x01":
					local = m[tagPublicKey]
					clientKey, err := ecdh.X25519().NewPublicKey(local)
					if err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					session.secret, err = accessoryKey.ECDH(clientKey)
					if err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					aead, _ := session.pairingCipher("Pair-Verify-Encrypt")
					id := []byte(credentials.AccessoryID)
					if wrongIdentity {
						id = []byte("different-accessory")
					}
					signed := encode(field{tagIdentifier, id}, field{tagSignature, ed25519.Sign(accessory, joined(peer, id, local))})
					pairingResponse(w, 2, field{tagPublicKey, peer},
						field{tagEncrypted, aead.Seal(nil, nonce("PV-Msg02"), signed, nil)})
				case "\x03":
					aead, _ := session.pairingCipher("Pair-Verify-Encrypt")
					proof, err := openMessage(aead, "PV-Msg03", m[tagEncrypted])
					id := proof[tagIdentifier]
					pub := ed25519.NewKeyFromSeed(credentials.ControllerSeed).Public().(ed25519.PublicKey)
					if err != nil || string(id) != credentials.ControllerID ||
						!ed25519.Verify(pub, joined(local, id, peer), proof[tagSignature]) {
						t.Errorf("控制端验证失败，错误 %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					serverDone <- verifyEncryptedControl(w, session)
				default:
					w.WriteHeader(http.StatusBadRequest)
				}
			})
			verified, err := PairVerify(t.Context(), client, credentials)
			if wrongIdentity {
				if err == nil || !strings.Contains(err.Error(), "身份验证失败") {
					t.Fatalf("错误接收端身份得到 %v", err)
				}
				return
			}
			if err != nil || len(verified.AudioKey()) != 32 {
				t.Fatalf("pair-verify 错误 %v", err)
			}
			r, err := client.Do(t.Context(), "OPTIONS", "*", nil, nil)
			if err != nil || r.StatusCode != 200 {
				t.Fatalf("升级后的加密 RTSP 错误 %v", err)
			}
			if err := <-serverDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func verifyEncryptedControl(w http.ResponseWriter, session Session) error {
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 3\r\n\r\n" + string(encode(field{tagState, []byte{4}})))
	if err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}
	read, err := encryptedRequest(rw.Reader, session)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(string(read), "OPTIONS * RTSP/1.0\r\nCSeq: 3\r\n") {
		return fmt.Errorf("加密 RTSP 请求 = %q", read)
	}
	key, _ := session.DeriveKey("Control-Salt", "Control-Read-Encryption-Key")
	aead, _ := chacha20poly1305.New(key)
	plain := []byte("RTSP/1.0 200 OK\r\nCSeq: 3\r\nContent-Length: 0\r\n\r\n")
	prefix := binary.LittleEndian.AppendUint16(nil, uint16(len(plain)))
	_, err = conn.Write(aead.Seal(bytes.Clone(prefix), make([]byte, 12), plain, prefix))
	return err
}

func encryptedRequest(r *bufio.Reader, session Session) ([]byte, error) {
	key, _ := session.DeriveKey("Control-Salt", "Control-Write-Encryption-Key")
	aead, _ := chacha20poly1305.New(key)
	prefix := make([]byte, 2)
	if _, err := io.ReadFull(r, prefix); err != nil {
		return nil, err
	}
	packet := make([]byte, int(binary.LittleEndian.Uint16(prefix))+aead.Overhead())
	if _, err := io.ReadFull(r, packet); err != nil {
		return nil, err
	}
	return aead.Open(nil, make([]byte, 12), packet, prefix)
}
