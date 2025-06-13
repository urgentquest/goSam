package gosam

import (
	"bufio"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-i2p/i2pkeys"
	//samkeys "github.com/go-i2p/gosam/compat"
)

// A Client represents a single Connection to the SAM bridge
type Client struct {
	host     string
	port     string
	fromport string
	toport   string
	user     string
	pass     string

	SamConn   net.Conn       // Control socket
	SamDGConn net.PacketConn // Datagram socket
	rd        *bufio.Reader
	//	d         *Client

	sigType     string
	destination string

	inLength   uint
	inVariance int
	inQuantity uint
	inBackups  uint

	outLength   uint
	outVariance int
	outQuantity uint
	outBackups  uint

	dontPublishLease bool
	encryptLease     bool
	leaseSetEncType  string

	reduceIdle         bool
	reduceIdleTime     uint
	reduceIdleQuantity uint

	closeIdle     bool
	closeIdleTime uint

	compress bool

	debug bool
	mutex sync.Mutex
	//NEVER, EVER modify lastaddr or id yourself. They are used internally only.
	id     int32
	sammin int
	sammax int
}

// SAMsigTypes is a slice of the available signature types
var SAMsigTypes = []string{
	"SIGNATURE_TYPE=DSA_SHA1",
	"SIGNATURE_TYPE=ECDSA_SHA256_P256",
	"SIGNATURE_TYPE=ECDSA_SHA384_P384",
	"SIGNATURE_TYPE=ECDSA_SHA512_P521",
	"SIGNATURE_TYPE=EdDSA_SHA512_Ed25519",
}

var ValidSAMCommands = []string{
	"HELLO",
	"DEST",
	"NAMING",
	"SESSION",
	"STREAM",
}

var (
	i2pB64enc *base64.Encoding = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-~")
	i2pB32enc *base32.Encoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567")
)

// NewDefaultClient creates a new client, connecting to the default host:port at localhost:7656
func NewDefaultClient() (*Client, error) {
	return NewClient("localhost:7656")
}

// NewClient creates a new client, connecting to a specified port
func NewClient(addr string) (*Client, error) {
	return NewClientFromOptions(SetAddr(addr))
}

func NewID() int32 {
	id := rand.Int31n(math.MaxInt32)
	fmt.Printf("Initializing new ID: %d\n", id)
	return id
}

// NewID generates a random number to use as an tunnel name
func (c *Client) NewID() int32 {
	if c.id == 0 {
		c.id = NewID()
	}
	return c.id
}

// Destination returns the full destination of the local tunnel
func (c *Client) Destination() string {
	return c.destination
}

// Base32 returns the base32 of the local tunnel
func (c *Client) Base32() string {
	//	hash := sha256.New()
	b64, err := i2pB64enc.DecodeString(c.Base64())
	if err != nil {
		return ""
	}
	//hash.Write([]byte(b64))
	var s []byte
	for _, e := range sha256.Sum256(b64) {
		s = append(s, e)
	}
	return strings.ToLower(strings.Replace(i2pB32enc.EncodeToString(s), "=", "", -1))
}

func (c *Client) base64() []byte {
	if c.destination != "" {
		s, _ := i2pB64enc.DecodeString(c.destination)
		alen := binary.BigEndian.Uint16(s[385:387])
		return s[:387+alen]
	}
	return []byte("")
}

// Base64 returns the base64 of the local tunnel
func (c *Client) Base64() string {
	return i2pB64enc.EncodeToString(c.base64())
}

// NewClientFromOptions creates a new client, connecting to a specified port
func NewClientFromOptions(opts ...func(*Client) error) (*Client, error) {
	var c Client
	c.host = "127.0.0.1"
	c.port = "7656"
	c.inLength = 3
	c.inVariance = 0
	c.inQuantity = 3
	c.inBackups = 1
	c.outLength = 3
	c.outVariance = 0
	c.outQuantity = 3
	c.outBackups = 1
	c.dontPublishLease = true
	c.encryptLease = false
	c.reduceIdle = false
	c.reduceIdleTime = 300000
	c.reduceIdleQuantity = 1
	c.closeIdle = true
	c.closeIdleTime = 600000
	c.debug = false
	c.sigType = SAMsigTypes[4]
	c.id = 0
	c.destination = ""
	c.leaseSetEncType = "4,0"
	c.fromport = ""
	c.toport = ""
	c.sammin = 0
	c.sammax = 1
	for _, o := range opts {
		if err := o(&c); err != nil {
			return nil, err
		}
	}
	c.id = c.NewID()
	conn, err := net.DialTimeout("tcp", c.samaddr(), 15*time.Minute)
	if err != nil {
		return nil, err
	}
	if c.debug {
		conn = WrapConn(conn)
	}
	c.SamConn = conn
	c.rd = bufio.NewReader(conn)
	return &c, c.hello()
}

// ID returns a the current ID of the client as a string
func (p *Client) ID() string {
	return fmt.Sprintf("%d", p.NewID())
}

// Addr returns the address of the client as a net.Addr
func (p *Client) Addr() net.Addr {
	keys := i2pkeys.I2PAddr(p.Destination())
	return keys
}

func (p *Client) LocalAddr() net.Addr {
	return p.Addr()
}

// LocalKeys returns the local keys of the client as a fully-fledged i2pkeys.I2PKeys
func (p *Client) PrivateAddr() i2pkeys.I2PKeys {
	//keys := i2pkeys.I2PAddr(p.Destination())
	keys := i2pkeys.NewKeys(i2pkeys.I2PAddr(p.base64()), p.Destination())
	return keys
}

// return the combined host:port of the SAM bridge
func (c *Client) samaddr() string {
	return fmt.Sprintf("%s:%s", c.host, c.port)
}

// send the initial handshake command and check that the reply is ok
func (c *Client) hello() error {
	var r *Reply
	var err error

	if c.getUser() == "" {
		r, err = c.sendCmd("HELLO VERSION MIN=3.%d MAX=3.%d\n", c.sammin, c.sammax)
	} else if c.getUser() != "" && c.getPass() == "" {
		r, err = c.sendCmd("HELLO VERSION MIN=3.%d MAX=3.%d %s\n", c.sammin, c.sammax, c.getUser())
	} else {
		r, err = c.sendCmd("HELLO VERSION MIN=3.%d MAX=3.%d %s %s\n", c.sammin, c.sammax, c.getUser(), c.getPass())
	}

	if err != nil {
		return err
	}

	if !r.IsOk() {
		return fmt.Errorf("handshake did not succeed\nReply:%+v", r)
	}

	return nil
}

// helper to send one command and parse the reply by sam
func (c *Client) sendCmd(str string, args ...any) (*Reply, error) {
	if err := validateCommand(str); err != nil {
		return nil, err
	}

	if _, err := fmt.Fprintf(c.SamConn, str, args...); err != nil {
		return nil, err
	}

	line, err := c.rd.ReadString('\n')
	if err != nil {
		return nil, err
	}

	r, err := parseReply(line)
	if err != nil {
		return nil, err
	}

	if err := c.validateReply(str, r); err != nil {
		return nil, fmt.Errorf("unrecogized reply: %+v\n%v", r, err)
	}

	return r, nil
}

func validateCommand(str string) error {
	topic, _, _ := strings.Cut(str, " ")
	for _, x := range ValidSAMCommands {
		if x == topic {
			return nil
		}
	}

	return fmt.Errorf("unsupported sam command %v", topic)

}

func (c *Client) validateReply(command string, reply *Reply) error {
	expectedTypesMap := map[string]string{
		"HELLO":   "REPLY",
		"DEST":    "REPLY",
		"NAMING":  "REPLY",
		"SESSION": "STATUS",
		"STREAM":  "STATUS",
	}
	commandTopic, _, _ := strings.Cut(command, " ")

	if commandTopic != reply.Topic {
		return fmt.Errorf("unrecogized reply topic. expecting: %v, got: %v", commandTopic, reply.Topic)
	}

	if expectedTypesMap[commandTopic] != reply.Type {
		return fmt.Errorf("unrecogized reply type. expecting: %v, got: %v", expectedTypesMap[commandTopic], reply.Type)
	}

	return nil
}

// Close the underlying socket to SAM
func (c *Client) Close() error {
	c.rd = nil
	return c.SamConn.Close()
}

// NewClient generates an exact copy of the client with the same options, but
// re-does all the handshaky business so that Dial can pick up right where it
// left off, should the need arise.
func (c *Client) NewClient(id int32) (*Client, error) {
	return NewClientFromOptions(
		SetHost(c.host),
		SetPort(c.port),
		SetDebug(c.debug),
		SetInLength(c.inLength),
		SetOutLength(c.outLength),
		SetInVariance(c.inVariance),
		SetOutVariance(c.outVariance),
		SetInQuantity(c.inQuantity),
		SetOutQuantity(c.outQuantity),
		SetInBackups(c.inBackups),
		SetOutBackups(c.outBackups),
		SetUnpublished(c.dontPublishLease),
		SetEncrypt(c.encryptLease),
		SetReduceIdle(c.reduceIdle),
		SetReduceIdleTime(c.reduceIdleTime),
		SetReduceIdleQuantity(c.reduceIdleQuantity),
		SetCloseIdle(c.closeIdle),
		SetCloseIdleTime(c.closeIdleTime),
		SetCompression(c.compress),
		setid(id),
	)
}
