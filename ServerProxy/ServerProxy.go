// ServerProxy project main.go
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
type ServerProxy struct {
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
func (this ServerProxy) SRVFAIL(w dns.ResponseWriter, req *dns.Msg) {
	m := new(dns.Msg)
	m.SetRcode(req, dns.RcodeServerFailure)
	w.WriteMsg(m)
}

/*
For fragmentation, we use a naive algorithm.

We use the same header for every fragment, and include the same EDNS0
section in every additional section.

We add one RR at a time, until our fragment is larger than 512 bytes,
then we remove the last RR so that it fits in the 512 byte size limit.

If we discover that one of the fragments ends up with 0 RR in it (for
example because a single RR is too big), then we return a single
truncated response instead of the set of fragments.

We could perhaps make the process of building fragments faster by
bisecting the set of RR that we include in an answer. So, if we have 8
RR we could try all, then if that is too big, 4 RR, and if that fits
then 6 RR, until an optimal set of RR is found.

We could also possibly produce a smaller set of responses by
optimizing how we combine RR. Just taking account the various sizes is
the same as the bin packing problem, which is NP-hard:

  https://en.wikipedia.org/wiki/Bin_packing_problem

While some non-optimal but reasonable heuristics exist, in the case of
DNS we would have to use some sophisticated algorithm to also consider
name compression.
*/
func frag(reply *dns.Msg) []dns.Msg {
	// create a return value
	all_frags := []dns.Msg{}
	HasEdns0 := true
	// get each RR section and save a copy out
	remaining_answer := make([]dns.RR, len(reply.Answer))
	copy(remaining_answer, reply.Answer)
	remaining_ns := make([]dns.RR, len(reply.Ns))
	copy(remaining_ns, reply.Ns)
	remaining_extra := make([]dns.RR, len(reply.Extra))
	copy(remaining_extra, reply.Extra)

	// if we don't have EDNS0 in the packet, add it now
	if reply.IsEdns0() == nil {
		reply.SetEdns0(512, false)
	}

	// the EDNS option for later use
	var edns0_rr dns.RR = nil

	// remove the EDNS0 option from our additional ("extra") section
	// (we will include it separately on every fragment)
	for ofs, r := range remaining_extra {
		// found the EDNS option
		if r.Header().Rrtype == dns.TypeOPT {
			// save the EDNS option
			edns0_rr = r
			// remove from the set of extra RR
			remaining_extra = append(remaining_extra[0:ofs], remaining_extra[ofs+1:]...)
			// in principle we should only have one EDNS0 section
			break
		}
	}

	if edns0_rr == nil {
		log.Printf("Server reply missing EDNS0 option")
		return []dns.Msg{}
		//HasEdns0 = false
	}

	// now build fragments
	for {
		// make a shallow copy of our reply packet, and prepare space for our RR
		frag := *reply
		frag.Answer = []dns.RR{}
		frag.Ns = []dns.RR{}
		frag.Extra = []dns.RR{}

		// add our custom EDNS0 option (needed in every fragment)
		local_opt := new(dns.EDNS0_LOCAL)
		local_opt.Code = dns.EDNS0LOCALSTART + 1
		local_opt.Data = []byte{0, 0}
		if HasEdns0 == true {
			edns0_rr_copy := dns.Copy(edns0_rr)
			edns0_rr_copy.(*dns.OPT).Option = append(edns0_rr_copy.(*dns.OPT).Option, local_opt)
			frag.Extra = append(frag.Extra, edns0_rr_copy)
		}
		//if HasEdns0 == false {
		//	frag.Extra = append(frag.Extra, local_opt)
		//}

		// add as many RR to the answer as we can
		for len(remaining_answer) > 0 {
			frag.Answer = append(frag.Answer, remaining_answer[0])
			if frag.Len() <= 512 {
				// if the new answer fits, then remove it from our remaining list
				remaining_answer = remaining_answer[1:]
			} else {
				// otherwise we are full, remove it from our fragment and stop
				frag.Answer = frag.Answer[0 : len(frag.Answer)-1]
				break
			}
		}
		for len(remaining_ns) > 0 {
			frag.Ns = append(frag.Ns, remaining_ns[0])
			if frag.Len() <= 512 {
				// if the new answer fits, then remove it from our remaining list
				remaining_ns = remaining_ns[1:]
			} else {
				// otherwise we are full, remove it from our fragment and stop
				frag.Ns = frag.Ns[0 : len(frag.Ns)-1]
				break
			}
		}
		for len(remaining_extra) > 0 {
			frag.Extra = append(frag.Extra, remaining_extra[0])
			if frag.Len() <= 512 {
				// if the new answer fits, then remove it from our remaining list
				remaining_extra = remaining_extra[1:]
			} else {
				// otherwise we are full, remove it from our fragment and stop
				frag.Extra = frag.Extra[0 : len(frag.Extra)-1]
				break
			}
		}

		// check to see if we didn't manage to add any RR
		if (len(frag.Answer) == 0) && (len(frag.Ns) == 0) && (len(frag.Extra) == 1) {
			// TODO: test this :)
			// return a single truncated fragment without any RR
			frag.MsgHdr.Truncated = true
			frag.Extra = []dns.RR{}
			return []dns.Msg{frag}
		}

		// add to our list of fragments
		all_frags = append(all_frags, frag)
		// if we have finished all remaining sections, we are done
		if (len(remaining_answer) == 0) && (len(remaining_ns) == 0) && (len(remaining_extra) == 0) {
			break
		}
	}

	// fix up our fragments so they have the correct sequence and length values
	for n, frag := range all_frags {
		frag_edns0 := frag.IsEdns0()
		for _, opt := range frag_edns0.Option {
			if opt.Option() == dns.EDNS0LOCALSTART+1 {
				opt.(*dns.EDNS0_LOCAL).Data = []byte{byte(len(all_frags)), byte(n)}
			}
		}
	}

	// return our fragments
	return all_frags
}

// our ServeDNS interface, which gets invoked on every DNS message
func (this ServerProxy) ServeDNS(w dns.ResponseWriter, request *dns.Msg) {
	// see if we have our groovy custom EDNS0 option
	client_supports_appfrag := false
	opt := request.IsEdns0()
	if opt != nil {
		for ofs, e := range opt.Option {
			if e.Option() == dns.EDNS0LOCALSTART {
				_D("%s QID:%d found EDNS0LOCALSTART", w.RemoteAddr(), request.Id)
				client_supports_appfrag = true
				// go ahead and use the maximum UDP size for the local communication
				// with our server
				opt.SetUDPSize(65535)
				// remove the fragmentation option
				opt.Option = append(opt.Option[0:ofs], opt.Option[ofs+1:]...)
				// in principle we should only have one of these options
				break
			}
		}
	}

	// proxy the query
	c := new(dns.Client)
	c.ReadTimeout = this.timeout
	c.WriteTimeout = this.timeout
	response, rtt, err := c.Exchange(request, this.SERVERS[rand.Intn(this.s_len)])
	if err != nil {
		_D("%s QID:%d error proxying query: %s", w.RemoteAddr(), request.Id, err)
		this.SRVFAIL(w, request)
		return
	}
	_D("%s QID:%d request took %s", w.RemoteAddr(), request.Id, rtt)

	// if the client does not support fragmentation, we just send the response back and finish
	if !client_supports_appfrag {
		_D("%s QID:%d sending raw response to client", w.RemoteAddr(), request.Id)
		w.WriteMsg(response)
		return
	}

	// otherwise lets get our fragments
	all_frags := frag(response)

	// send our fragments
	for n, frag := range all_frags {
		_D("%s QID:%d sending fragment %d", w.RemoteAddr(), request.Id, n)
		w.WriteMsg(&frag)
	}
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

	flag.StringVar(&S_SERVERS, "proxy", "127.0.0.1:53", "we proxy requests to those servers")
	flag.StringVar(&S_LISTEN, "listen", "8000", "listen on (both tcp and udp)")
	flag.StringVar(&S_ACCESS, "access", "0.0.0.0/0", "allow those networks, use 0.0.0.0/0 to allow everything")
	flag.IntVar(&timeout, "timeout", 5, "timeout")
	flag.Int64Var(&expire_interval, "expire_interval", 300, "delete expired entries every N seconds")
	flag.BoolVar(&DEBUG, "debug", false, "enable/disable debug")
	flag.Int64Var(&max_entries, "max_cache_entries", 2000000, "max cache entries")

	flag.Parse()
	servers := strings.Split(S_SERVERS, ",")
	proxyer := ServerProxy{
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
		_D("listening @ :%s\n", addr)
		go func() {
			if err := dns.ListenAndServe(":"+addr, "udp", proxyer); err != nil {
				log.Fatal(err)
			}
		}()

		go func() {
			if err := dns.ListenAndServe(":"+addr, "tcp", proxyer); err != nil {
				log.Fatal(err)
			}
		}()
	}

	for {
		proxyer.NOW = time.Now().UTC().Unix()
		time.Sleep(time.Duration(1) * time.Second)
	}
}
