package ciphers

import (
	"crypto/rand"
	"io"
	"net"
	"os"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/rc452860/vnet/log"
	"github.com/rc452860/vnet/utils/datasize"
)

var logging *log.Logging

var ()

func init() {

}

//TODO goroutine pool
func Test_Packet(t *testing.T) {
	logging = log.GetLogger("root")
	logging.Info("aa")
	listener, err := net.ListenPacket("udp", "0.0.0.0:8080")
	if err != nil {
		logging.Err(err)
	}
	dlistener, err := CipherPacketDecorate("killer", "aes-128-gcm", listener)
	if err != nil {
		logging.Err(err)
	}
	buf := make([]byte, 64*1024)
	go func() {
		for {
			_, _, err := dlistener.ReadFrom(buf)
			if err != nil {
				logging.Err(err)
				continue
			}
			// logging.Info("len: %d,addr %v,data: %s\n", n, addr, string(buf[:n]))
		}
	}()
	logging.Info("开始发送数据:")
	raddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:8080")
	if err != nil {
		logging.Err(err)
		return
	}
	conn, err := net.ListenPacket("udp", "0.0.0.0:8081")
	if err != nil {
		logging.Err(err)
		return
	}
	dconn, err := CipherPacketDecorate("killer", "aes-128-gcm", conn)
	if err != nil {
		logging.Err(err)
		return
	}
	tmp := make([]byte, 4*1024)
	if _, err := io.ReadFull(rand.Reader, tmp); err != nil {
		t.Error(err)
	}
	f, _ := os.Create("a.pprof")
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()
	start := time.Now()
	var count uint64 = 0
	for time.Now().Second()-start.Second() < 5 {
		count += 4096
		go dconn.WriteTo(tmp, raddr)
	}
	size, _ := datasize.HumanSize(count / uint64(5))
	logging.Info("%s per second", size)

	time.Sleep(1 * time.Second)
}
