package gtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	_ "embed"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/caddyserver/certmagic"
)

var Https = certmagic.HTTPS

func HTTPS(domainNames []string, mux http.Handler) error {
	return certmagic.HTTPS(domainNames, mux)
}
func Listen(domainNames []string) (net.Listener, error) {
	return certmagic.Listen(domainNames)
}
func TLS(domainNames []string) (*tls.Config, error) {
	return certmagic.TLS(domainNames)
}

//go:embed ssl/gospider.crt
var CrtFile []byte

//go:embed ssl/gospider.key
var KeyFile []byte

func SplitHostPort(address string) (string, int, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, err
	}
	portnum, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, err
	}
	if 1 > portnum || portnum > 0xffff {
		return "", 0, errors.New("port number out of range " + port)
	}
	return host, portnum, nil
}
func ParseHost(host string) (net.IP, int) {
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return ip4, 4
		} else if ip6 := ip.To16(); ip6 != nil {
			return ip6, 6
		}
	}
	return nil, 0
}
func VerifyProxy(proxyUrl string) (*url.URL, error) {
	proxy, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, err
	}
	switch proxy.Scheme {
	case "http", "socks5", "https":

		return proxy, nil
	default:
		return nil, errors.New("unsupported proxy scheme: " + proxy.Scheme)
	}
}
func GetServerName(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func generateSerialNumber() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}
func CreateRootCert() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	serialNumber, err := generateSerialNumber()
	if err != nil {
		return nil, nil, err
	}
	// 创建 CA 证书模板
	caTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Country:            []string{"CN"},
			Organization:       []string{"MITM Proxy Co"},
			OrganizationalUnit: []string{"MITM"},
			CommonName:         "MITM Proxy CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(100, 0, 0), // 100年有效期
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	// 生成 CA 私钥
	caPrivKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	// 生成自签名 CA 证书
	caCertBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return nil, nil, err
	}
	caCert, _ := x509.ParseCertificate(caCertBytes)
	return caCert, caPrivKey, nil
}
func CreateCertWithAddr(address ...net.IP) (tls.Certificate, error) {
	caCert, err := LoadCert(CrtFile)
	if err != nil {
		return tls.Certificate{}, err
	}
	caPrivKey, err := LoadCertKey(KeyFile)
	if err != nil {
		return tls.Certificate{}, err
	}
	serialNumber, err := generateSerialNumber()
	if err != nil {
		return tls.Certificate{}, err
	}
	// 服务器证书模板
	serverTemplate := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Country:            []string{"CN"},
			Organization:       []string{"MITM Proxy Co"},
			OrganizationalUnit: []string{"MITM"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(100, 0, 0), // 1年有效期
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           address,
	}

	// 生成服务器私钥（ECDSA 更快，RSA 兼容性更好）
	serverPrivKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	// 用 CA 签发服务器证书
	serverCertBytes, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverPrivKey.PublicKey, caPrivKey)
	if err != nil {
		return tls.Certificate{}, err
	}
	serverCert, _ := x509.ParseCertificate(serverCertBytes)
	return tls.X509KeyPair(GetCertData(serverCert), GetCertKeyData(serverPrivKey))
}
func MergeCert(cert *x509.Certificate, key *ecdsa.PrivateKey) (tls.Certificate, error) {
	return tls.X509KeyPair(GetCertData(cert), GetCertKeyData(key))
}

func GetCertData(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
}
func GetCertKeyData(key *ecdsa.PrivateKey) []byte {
	keyDer, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDer})
}
func LoadCertKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	return x509.ParseECPrivateKey(block.Bytes)
}
func LoadCert(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	return x509.ParseCertificate(block.Bytes)
}

type AddrType int

const (
	Auto AddrType = iota
	Ipv4
	Ipv6
)

func ParseIp(ip net.IP) AddrType {
	if ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			return 4
		} else if ip6 := ip.To16(); ip6 != nil {
			return 6
		}
	}
	return 0
}
func GetHost(addrTypes ...AddrType) net.IP {
	hosts := GetHosts(addrTypes...)
	if len(hosts) == 0 {
		return nil
	} else {
		return hosts[0]
	}
}
func GetHosts(addrTypes ...AddrType) []net.IP {
	var addrType AddrType
	if len(addrTypes) > 0 {
		addrType = addrTypes[0]
	}
	result := []net.IP{}
	lls, err := net.InterfaceAddrs()
	if err != nil {
		return result
	}
	for _, ll := range lls {
		mm, ok := ll.(*net.IPNet)
		if ok && mm.IP.IsPrivate() {
			if addrType == 0 || ParseIp(mm.IP) == addrType {
				result = append(result, mm.IP)
			}
		}
	}
	return result
}
