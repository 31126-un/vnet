package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rc452860/vnet/utils/addr"
	"github.com/rc452860/vnet/utils/goroutine"

	"github.com/pkg/errors"

	"github.com/rc452860/vnet/common/log"
	"github.com/rc452860/vnet/common/pool"
	"github.com/rc452860/vnet/network/ciphers"
	"github.com/rc452860/vnet/network/conn"
	"github.com/rc452860/vnet/record"
	"github.com/rc452860/vnet/socks"
	"golang.org/x/time/rate"
)

type mode int

var (
	logging *log.Logging
	// ShadowsocksServerList Global map for store shadowsocks proxy
	ShadowsocksServerList map[string]*ShadowsocksProxy
)

func init() {
	logging = log.GetLogger("root")
}

// ShadowsocksProxy is respect shadowsocks proxy server
// it have Start and Stop method to control proxy
type ShadowsocksProxy struct {
	*ProxyService `json:"-,omitempty"`
	Host          string `json:"host,omitempty"`
	Port          int    `json:"port,omitempty"`
	Method        string `json:"method,omitempty"`
	Password      string `json:"password,omitempty"`
	ShadowsocksArgs
	ReadLimiter  *rate.Limiter `json:"read_limit,omitempty"`
	WriteLimiter *rate.Limiter `json:"write_limit,omitempty"`
}

// ShadowsocksArgs is ShadowsocksProxy arguments
type ShadowsocksArgs struct {
	ConnectTimeout time.Duration `json:"connect_timeout,omitempty"`
	Limit          uint64        `json:"limit"`
	TCPSwitch      string        `json:"tcp_switch"`
	UDPSwitch      string        `json:"udp_switch"`
	Data           interface{}   `json:"-"`
}

func (sa ShadowsocksArgs) CompareTo(target ShadowsocksArgs) bool {
	equal := true
	if sa.ConnectTimeout != target.ConnectTimeout && target.ConnectTimeout != 0 {
		equal = false
	}
	if sa.Limit != target.Limit {
		equal = false
	}
	if sa.TCPSwitch != target.TCPSwitch {
		equal = false
	}

	if sa.UDPSwitch != target.UDPSwitch {
		equal = false
	}
	return equal
}

// NewShadowsocks is new ShadowsocksProxy object
func NewShadowsocks(host string, method string, password string, port int, ssarg ShadowsocksArgs) (*ShadowsocksProxy, error) {
	ss := &ShadowsocksProxy{
		ProxyService:    NewProxyService(),
		Host:            host,
		Method:          method,
		Password:        password,
		Port:            port,
		ShadowsocksArgs: ssarg,
	}
	if ss.TCPSwitch == "" {
		ss.TCPSwitch = "true"
	}
	if ss.UDPSwitch == "" {
		ss.UDPSwitch = "true"
	}
	ss.ConfigLimit()
	ss.ConfigTimeout()
	return ss, nil
}

// ConfigLimit config shadowsocks traffic limit
func (s *ShadowsocksProxy) ConfigLimit() {
	if s.Limit == 0 {
		log.Info("port %v config limit is 0, the mean is no limit", s.Port)
		s.ReadLimiter = nil
		s.WriteLimiter = nil
	}
	s.ReadLimiter = rate.NewLimiter(rate.Limit(s.Limit), int(s.Limit))
	s.WriteLimiter = rate.NewLimiter(rate.Limit(s.Limit), int(s.Limit))
}

// ConfigTimeout is config shadowsocks timeout
func (s *ShadowsocksProxy) ConfigTimeout() {
	if s.ConnectTimeout == 0 {
		s.ConnectTimeout = 3 * time.Second
	}
}

func (s *ShadowsocksProxy) ChangeLimit(limit uint64) {
	log.Info("port %v change limit from %v to %v", s.Port, s.Limit, limit)
	s.Limit = limit
	s.ConfigLimit()
}

func (s *ShadowsocksProxy) ChangeTimeout(connectTime time.Duration) {
	if connectTime == 0 {
		return
	}
	log.Info("port %v change connection timeout from %v to %v", s.Port, s.ConnectTimeout, connectTime)
	s.ConnectTimeout = connectTime
	s.ConfigTimeout()
}

func (s *ShadowsocksProxy) ChangeMethod(method string) {
	log.Info("port %v change method from %s to %s", s.Port, s.Method, method)
	s.Method = method
}

func (s *ShadowsocksProxy) ChangePassword(password string) {
	log.Info("port %v change password from %s to %s", s.Port, s.Password, password)
	s.Password = password
}

// Start proxy
func (s *ShadowsocksProxy) Start() error {
	log.Info("start shadowsocks tcp:%s udp:%s method:%s passwd:%s limit:%v on:%v", s.TCPSwitch, s.UDPSwitch, s.Method, s.Password, s.ShadowsocksArgs.Limit, s.Port)
	s.ConfigLimit()
	s.ConfigTimeout()
	if s.TCPSwitch == "true" || s.UDPSwitch == "true" {
		s.ProxyService.Start()
	}

	if s.TCPSwitch == "true" {
		if err := s.startTCP(); err != nil {
			log.Err(err)
			return err
		}
	}

	if s.UDPSwitch == "true" {
		if err := s.startUDP(); err != nil {
			log.Err(err)
			return err
		}
	}

	return nil
}

// Stop proxy
func (s *ShadowsocksProxy) Stop() error {
	start := time.Now()
	defer log.Info("proxy stop %v consume %v", s.Port, time.Since(start).Nanoseconds())
	return s.ProxyService.Stop()

}

// statistics tcpUpload traffic
func (s *ShadowsocksProxy) tcpUpload(con conn.IConn, up uint64) {
	message := record.Traffic{
		ConnectionPair: record.ConnectionPair{
			ProxyAddr:  con.LocalAddr(),
			ClientAddr: con.RemoteAddr(),
		},
		Network: con.GetNetwork(),
		Up:      up,
	}

	s.ProxyService.MessageRoute <- message
}

// statics tcpDownload traffic
func (s *ShadowsocksProxy) tcpDownload(con conn.IConn, down uint64) {
	message := record.Traffic{
		ConnectionPair: record.ConnectionPair{
			ProxyAddr:  con.LocalAddr(),
			ClientAddr: con.RemoteAddr(),
		},
		Network: con.GetNetwork(),
		Down:    down,
	}

	s.ProxyService.MessageRoute <- message
}

// start shadowsocks tcp proxy service
func (s *ShadowsocksProxy) startTCP() error {
	serverAddr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	tcpAddr, err := net.ResolveTCPAddr("tcp", serverAddr)
	if err != nil {
		logging.Error(err.Error())
		return err
	}
	server, err := net.ListenTCP("tcp", tcpAddr)
	// logging.Info("listening TCP on %s", addr)
	if err != nil {
		logging.Error(err.Error())
		return errors.Cause(err)
	}
	s.TCP = server

	go goroutine.Protect(func() {
		defer s.clearTCP()
		for {
			select {
			case <-s.ProxyService.Done():
				return
			default:
			}
			server.SetDeadline(time.Now().Add(s.ConnectTimeout))
			lcon, err := server.Accept()
			if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
				continue
			}

			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				logging.Error(err.Error())
				continue
			}

			go goroutine.Protect(func() {
				defer lcon.Close()
				/** 默认装饰器 */
				lcd, err := conn.DefaultDecorate(lcon, conn.TCP)
				if err != nil {
					logging.Err(err)
					return
				}
				/** 去皮流量记录装饰器 */
				lcd, err = conn.TrafficDecorate(lcd, s.tcpUpload, s.tcpDownload)
				if err != nil {
					logging.Err(err)
					return
				}
				/** 限流装饰器 */
				lcd, _ = conn.TrafficLimitDecorate(lcd, s.ReadLimiter, s.WriteLimiter)

				/** 加密装饰器 */
				lcd, err = ciphers.CipherDecorate(s.Password, s.Method, lcd)
				if err != nil {
					logging.Err(err)
					return
				}

				/** 读取目标地址 */
				targetAddr, err := socks.ReadAddr(lcd)
				if err != nil {
					log.Error("tcp:%v read target address error %s. (maybe the crypto method wrong configuration)", addr.GetPortFromAddr(server.Addr()), err.Error())
					return
				}
				resloveAddr, err := s.dnsReslove(targetAddr)
				if err != nil {
					log.Err(err)
					return
				}
				rc, err := net.Dial("tcp", resloveAddr)
				if err != nil {
					logging.Error("connect target:%s error cause: %v", targetAddr, err)
					return
				}
				defer rc.Close()

				s.ConnectionStage(s.TCP.Addr(), lcd.RemoteAddr(), rc.RemoteAddr(), targetAddr)

				rc.(*net.TCPConn).SetKeepAlive(true)

				/** 默认装饰器 */
				rcd, err := conn.DefaultDecorate(rc, conn.TCP)
				if err != nil {
					logging.Err(err)
					return
				}

				_, _, err = relayTCP(lcd, rcd)
				if err != nil {
					if err, ok := err.(net.Error); ok && err.Timeout() {
						return // ignore i/o timeout
					}
					logging.Error("relay error: %v", err)
				}
			})
		}
	})
	return nil
}

// relay copies between left and right bidirectionally. Returns number of
// bytes copied from right to left, from left to right, and any error occurred.
func relayTCP(left, right net.Conn) (int64, int64, error) {
	type res struct {
		N   int64
		Err error
	}
	ch := make(chan res)
	defer func() {
		if e := recover(); e != nil {
			log.Error("panic in timedCopy: %v", e)
		}
	}()

	go goroutine.Protect(func() {
		n, err := io.Copy(right, left)
		right.SetDeadline(time.Now()) // wake up the other goroutine blocking on right
		left.SetDeadline(time.Now())  // wake up the other goroutine blocking on left
		ch <- res{n, err}
	})

	n, err := io.Copy(left, right)
	right.SetDeadline(time.Now()) // wake up the other goroutine blocking on right
	left.SetDeadline(time.Now())  // wake up the other goroutine blocking on left
	rs := <-ch

	if err == nil {
		err = rs.Err
	}
	return n, rs.N, errors.Cause(err)
}

// udp upload traffic count
func (s *ShadowsocksProxy) udpUpload(laddr, raddr net.Addr, n uint64) {
	message := record.Traffic{
		ConnectionPair: record.ConnectionPair{
			ProxyAddr:  laddr,
			ClientAddr: raddr,
		},
		Network: laddr.Network(),
		Up:      n,
	}

	s.ProxyService.MessageRoute <- message
}

// udp download traffic count
func (s *ShadowsocksProxy) udpDownload(laddr, raddr net.Addr, n uint64) {
	message := record.Traffic{
		ConnectionPair: record.ConnectionPair{
			ProxyAddr:  laddr,
			ClientAddr: raddr,
		},
		Network: laddr.Network(),
		Down:    n,
	}
	s.ProxyService.MessageRoute <- message
}

// Listen on addr for encrypted packets and basically do UDP NAT.
func (s *ShadowsocksProxy) startUDP() error {
	serverAddr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	server, err := net.ListenPacket("udp", serverAddr)
	if err != nil {
		logging.Error("UDP remote listen error: %v", err)
		return errors.Cause(err)
	}
	s.UDP = server
	// 去皮流量装饰器
	server = conn.PacketTrafficConnDecorate(server, s.udpUpload, s.udpDownload)

	nm := newNATmap(s.ConnectTimeout)
	buf := pool.GetBuf()
	defer pool.PutBuf(buf)

	// logging.Info("listening UDP on %s", addr)

	go goroutine.Protect(func() {
		defer s.clearUDP()
		for {
			select {
			case <-s.ProxyService.Done():
				return
			default:
			}
			serverWithCiphers, err := ciphers.CipherPacketDecorate(s.Password, s.Method, server)
			if err != nil {
				logging.Error("UDP CipherPacketDecorate init error: %v", err)
				continue
			}

			serverWithCiphers.SetDeadline(time.Now().Add(s.ConnectTimeout))
			n, raddr, err := serverWithCiphers.ReadFrom(buf)
			if opErr, ok := err.(*net.OpError); ok && opErr.Timeout() {
				continue
			}
			if err != nil {
				if strings.Contains(err.Error(), "use of closed network connection") {
					return
				}
				logging.Error("UDP remote read error: %v", err)
				continue
			}
			tgtAddr := socks.SplitAddr(buf[:n])
			if tgtAddr == nil {
				logging.Error("udp:%v read target address error. (maybe the crypto method wrong configuration)", addr.GetPortFromAddr(serverWithCiphers.LocalAddr()))
				continue
			}
			addr, err := s.dnsReslove(tgtAddr)
			if err != nil {
				log.Err(err)
				return
			}
			tgtUDPAddr, err := net.ResolveUDPAddr("udp", addr)
			if err != nil {
				logging.Error("failed to resolve target UDP address: %v", err)
				continue
			}

			s.ConnectionStage(s.UDP.LocalAddr(), raddr, tgtUDPAddr, tgtAddr)
			payload := buf[len(tgtAddr.Raw):n]

			pc := nm.Get(raddr.String())
			if pc == nil {
				pc, err = net.ListenPacket("udp", "")
				if err != nil {
					logging.Error("UDP remote listen error: %v", err)
					continue
				}

				nm.Add(raddr, serverWithCiphers, pc)
			}

			_, err = pc.WriteTo(payload, tgtUDPAddr) // accept only UDPAddr despite the signature
			if err != nil {
				logging.Error("UDP remote write error: %v", err)
				continue
			}
		}
	})
	return nil
}

// ConnectionStage event
func (s *ShadowsocksProxy) ConnectionStage(proxyAddr, client, target net.Addr, pr record.IProxyRequest) {
	pr = record.ConnectionProxyRequest{
		ConnectionPair: record.ConnectionPair{
			ClientAddr: client,
			ProxyAddr:  proxyAddr,
			TargetAddr: target,
		},
		IProxyRequest: pr,
	}
	s.MessageRoute <- pr
}

func (s *ShadowsocksProxy) dnsReslove(request record.IProxyRequest) (string, error) {
	return request.String(), nil
}

func (s *ShadowsocksProxy) String() string {
	result, err := json.Marshal(s)
	if err != nil {
		log.Err(err)
		return ""
	}
	return string(result)
}

// Packet NAT table
type natmap struct {
	sync.RWMutex
	m       map[string]net.PacketConn
	timeout time.Duration
}

func newNATmap(timeout time.Duration) *natmap {
	m := &natmap{}
	m.m = make(map[string]net.PacketConn)
	m.timeout = timeout
	return m
}

func (m *natmap) Get(key string) net.PacketConn {
	m.RLock()
	defer m.RUnlock()
	return m.m[key]
}

func (m *natmap) Set(key string, pc net.PacketConn) {
	m.Lock()
	defer m.Unlock()
	m.m[key] = pc
}

func (m *natmap) Del(key string) net.PacketConn {
	m.Lock()
	defer m.Unlock()

	pc, ok := m.m[key]
	if ok {
		delete(m.m, key)
		return pc
	}
	return nil
}

func (m *natmap) Add(peer net.Addr, dst, src net.PacketConn) {
	m.Set(peer.String(), src)
	go goroutine.Protect(func() {
		timedCopy(dst, peer, src, m.timeout)
		if pc := m.Del(peer.String()); pc != nil {
			pc.Close()
		}
	})
}

// copy from src to dst at target with read timeout
func timedCopy(dst net.PacketConn, target net.Addr, src net.PacketConn, timeout time.Duration) error {
	buf := pool.GetBuf()
	defer pool.PutBuf(buf)
	defer func() {
		if e := recover(); e != nil {
			log.Error("panic in timedCopy: %v", e)
		}
	}()

	for {
		src.SetReadDeadline(time.Now().Add(timeout))
		n, raddr, err := src.ReadFrom(buf)
		if err != nil {
			return errors.Cause(err)
		}

		srcAddr := socks.ParseAddr(raddr.String())
		srcAddrByte := srcAddr.Raw
		copy(buf[len(srcAddrByte):], buf[:n])
		copy(buf, srcAddrByte)
		_, err = dst.WriteTo(buf[:len(srcAddrByte)+n], target)

		if err != nil {
			return errors.Cause(err)
		}
	}
}

func (s *ShadowsocksProxy) clearUDP() {

}

func (s *ShadowsocksProxy) clearTCP() {

}
