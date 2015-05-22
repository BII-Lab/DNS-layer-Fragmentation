// ClientProxy
package main

import (
	"github.com/miekg/dns"
	"flag"
	"log"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

// flag whether we want to emit debug output
var DEBUG bool = false

// called for debug output
func _D(fmt string, v ...interface{}) {
	if DEBUG {
		log.Printf(fmt, v...)
	}
}

// this structure will be used by the dns.ListenAndServe() method
type ClientProxy struct {
	ACCESS      []*net.IPNet
	SERVERS     []string
	s_len       int
	entries     int64
	max_entries int64
	NOW         int64
	giant       *sync.RWMutex
	timeout     time.Duration
}

// SRVFAIL result for serious problems
func SRVFAIL(w dns.ResponseWriter, req *dns.Msg) {
	m := new(dns.Msg)
	m.SetRcode(req, dns.RcodeServerFailure)
	w.WriteMsg(m)
}

// wait for a matching reponse
func wait_for_response(w dns.ResponseWriter, conn *dns.Conn, request *dns.Msg) (response *dns.Msg) {
	for {
		response, err := conn.ReadMsg()
		// some sort of error reading reply
		if err != nil {
			_D("%s QID:%d error reading message: %s", w.RemoteAddr(), request.Id, err)
			SRVFAIL(w, request)
			return nil
		}
		// got a response, life is good
		if response.Id == request.Id {
			_D("%s QID:%d got reply", w.RemoteAddr(), request.Id)
			return response
		}
		// got a response, but it was for a different QID... ignore
		_D("%s QID:%d ignoring reply to wrong QID:%d", w.RemoteAddr(), request.Id, response.Id)
	}
}

// extract out the total fragments and sequence number from the EDNS0 informaton in a packet
func get_fragment_info(msg *dns.Msg) (num_frags int, sequence_num int) {
	num_frags = -1
	sequence_num = -1
	resp_edns0 := msg.IsEdns0()
	if resp_edns0 != nil {
		for _, opt := range resp_edns0.Option {
			if opt.Option() == dns.EDNS0LOCALSTART + 1 {
				num_frags = int(opt.(*dns.EDNS0_LOCAL).Data[0])
				sequence_num = int(opt.(*dns.EDNS0_LOCAL).Data[1])
				// we only expect this option to be here once
				break
			}
		}
	}
	return num_frags, sequence_num
}

func (this ClientProxy) ServeDNS(w dns.ResponseWriter, request *dns.Msg) {
	// if we don't have EDNS0 in the packet, add it now
	// TODO: in principle we should check packet size here, since we have made it bigger,
	//       but for this demo code we will just rely on most queries being really small
	proxy_req := *request
	opt := proxy_req.IsEdns0()
	var client_buf_size uint16
	if opt == nil {
		proxy_req.SetEdns0(512, false)
		client_buf_size = 512
		_D("%s QID:%d adding EDNS0 to packet", w.RemoteAddr(), request.Id)
		opt = proxy_req.IsEdns0()
	} else {
		client_buf_size = opt.UDPSize()
	}

	// add our custom EDNS0 option
	local_opt := new(dns.EDNS0_LOCAL)
	local_opt.Code = dns.EDNS0LOCALSTART
	opt.Option = append(opt.Option, local_opt)

	// create a connection to the server
	// XXX: for now we will only handle UDP - this will break in unpredictable ways in production!
	conn, err := dns.DialTimeout("udp", this.SERVERS[rand.Intn(len(this.SERVERS))], this.timeout)
	if err != nil {
		_D("%s QID:%d error setting up UDP socket: %s", w.RemoteAddr(), request.Id, err)
		SRVFAIL(w, request)
		return
	}
	defer conn.Close()

	// set our timeouts
	// TODO: we need to insure that our timeouts work like we expect
	conn.SetReadDeadline(time.Now().Add(this.timeout))
	conn.SetWriteDeadline(time.Now().Add(this.timeout))

	// send our query
	err = conn.WriteMsg(&proxy_req)
	if err != nil {
		_D("%s QID:%d error writing message: %s", w.RemoteAddr(), request.Id, err)
		SRVFAIL(w, request)
		return
	}

	// wait for our reply
	response := wait_for_response(w, conn, request)
	if response == nil {
		return
	}

	// get fragment information from first response (if any)
	num_frags, sequence_num := get_fragment_info(response)

	// if we did not have a fragmented response, send it to the client
	if num_frags == -1 {
	    w.WriteMsg(response)
	    return
	}

	// build a map to hold the fragments that we have received
	frags := map[int]dns.Msg{ sequence_num: *response }

	// wait for all fragments to arrive
	// duplicates overwrite previous packet, missing packets eventually timeout
	for len(frags) < num_frags {
		response := wait_for_response(w, conn, request)
		if response == nil {
			return
		}
	        _, sequence_num := get_fragment_info(response)
		// TODO: remove the extra EDNS0 option
		frags[sequence_num] = *response
	}

	// rebuild our original packet
	rebuilt_reply, ok := frags[0]
	if !ok {
		_D("%s QID:%d missing fragment 0", w.RemoteAddr(), request.Id)
		SRVFAIL(w, request)
		return
	}
	for n := 1; n < num_frags; n++ {
		frag, ok := frags[n]
		if !ok {
			_D("%s QID:%d missing fragment %d", w.RemoteAddr(), request.Id, n)
			SRVFAIL(w, request)
			return
		}
		rebuilt_reply.Answer = append(rebuilt_reply.Answer, frag.Answer...)
		rebuilt_reply.Ns = append(rebuilt_reply.Ns, frag.Ns...)
		for _, r := range frag.Extra {
			// remove EDNS0 present in fragments from final answer
			if r.Header().Rrtype != dns.TypeOPT {
				rebuilt_reply.Extra = append(rebuilt_reply.Extra, r)
			}
		}
	}

	// verify that we don't exceed the client buffer size
	if rebuilt_reply.Len() > int(client_buf_size) {
		// truncate if we need to
		// TODO: test this
		rebuilt_reply.MsgHdr.Truncated = true
		rebuilt_reply.Answer = []dns.RR{}
		rebuilt_reply.Ns = []dns.RR{}
		rebuilt_reply.Extra = []dns.RR{}
	}

	// send our rebuilt reply
	w.WriteMsg(&rebuilt_reply)
}

func main() {

	var (
		S_SERVERS       string
		S_LISTEN        string
		S_ACCESS        string
		timeout         int
		max_entries     int64
		expire_interval int64
	)
	flag.StringVar(&S_SERVERS, "proxy", "8.8.8.8:53,8.8.4.4:53", "we proxy requests to those servers")
	flag.StringVar(&S_LISTEN, "listen", "[::]:53", "listen on (both tcp and udp)")
	flag.StringVar(&S_ACCESS, "access", "127.0.0.0/8,10.0.0.0/8", "allow those networks, use 0.0.0.0/0 to allow everything")
	flag.IntVar(&timeout, "timeout", 5, "timeout")
	flag.Int64Var(&expire_interval, "expire_interval", 300, "delete expired entries every N seconds")
	flag.BoolVar(&DEBUG, "debug", false, "enable/disable debug")
	flag.Int64Var(&max_entries, "max_cache_entries", 2000000, "max cache entries")

	flag.Parse()
	servers := strings.Split(S_SERVERS, ",")
	proxyer := ClientProxy{
		giant:       new(sync.RWMutex),
		ACCESS:      make([]*net.IPNet, 0),
		SERVERS:     servers,
		s_len:       len(servers),
		NOW:         time.Now().UTC().Unix(),
		entries:     0,
		timeout:     time.Duration(timeout) * time.Second,
		max_entries: max_entries}

	for _, mask := range strings.Split(S_ACCESS, ",") {
		_, cidr, err := net.ParseCIDR(mask)
		if err != nil {
			panic(err)
		}
		_D("added access for %s\n", mask)
		proxyer.ACCESS = append(proxyer.ACCESS, cidr)
	}
	for _, addr := range strings.Split(S_LISTEN, ",") {
		_D("listening @ %s\n", addr)
		go func() {
			if err := dns.ListenAndServe(addr, "udp", proxyer); err != nil {
				log.Fatal(err)
			}
		}()

		go func() {
			if err := dns.ListenAndServe(addr, "tcp", proxyer); err != nil {
				log.Fatal(err)
			}
		}()
	}

	for {
		proxyer.NOW = time.Now().UTC().Unix()
		time.Sleep(time.Duration(1) * time.Second)
	}
}
