#DNS-layer-Fragmentation

Introduction
------------

This code implements proxies for DNS application-level fragmentation,
based on a IETF draft :

	https://tools.ietf.org/id/draft-muks-dns-message-fragments-00.txt

In IPv4, DNS datagrams that do not fit into a single physical packet are
sometimes split into several smaller packets and reassembled by the
network. The idea with these proxies is to explore splitting DNS messages 
in the protocol itself, so they will not by fragmented by the IP layer.

This is a proof of concept setup, which provides a client proxy and a
server proxy. The data flow works like this:

* Query:
  * DNS query packets arrive at the client proxy, which listens on the
    well-known port 53.
  * The client proxy then modified the parckets with a new EDNS0 OPT code and sends the DNS packets over the network to a custom port on the server proxy
  * The packets arrive at the server proxy, which understand the new OPT code, then sends them to
    port 53 of the actual DNS server.
* Reply:
  * The DNS server sends replies, which go back to the server proxy.
  * The server proxy fragments the replies (if necessary), and then
    sends these reply fragments back to the client proxy.
  * The client proxy reassembles the full reply from the fragments,
    and then sends the reply back to the original query source.

In principle, the server proxy could be put in front of any DNS
authoritative server to provide support for DNS application-level
fragmentation. Using the client proxy in front of a normal DNS
resolver would require a bit more work.

Construction
------------

To compile the code, make sure your have install golang 1.4 version and  already compiled go dns lib written by miekg (https://github.com/miekg/dns).You can find introduction's in miekg's github. To simply get and compile miekg's package in golang, just run:

	go get github.com/miekg/dns
	go build github.com/miekg/dns

Then you can get the code in this repository by:

	go get github.com/BII-Lab/DNS-layer-Fragmentation


Server Installation
-------------------

The server proxy will need a working resolver on your server. The server should be reachable by UDP and TCP, and you should have a clear ICMP path to it, as well as full MTU (1500 octets or larger) and the ability to receive. And the server proxy need be assigned a port to listen on as the port for this proxy.

1. compile the ServerProxy.

	go build github.com/BII-Lab/DNS-layer-Fragmentation/ServerProxy

2. make sure you have a working resovler.

3. run the ServerProxy as 
	
	./ServerProxy -proxy "[your resovler ip address]" -listen ":[your assigned port]"
For exmaple, the resolver instance is running in the same server, we can use loopback address 
	
	./ServerProxy -proxy "127.0.0.1:53" -listen "10000"

Client Installation
-------------------

The ClientProxy will listen on the port assigned (defaul port is 53). And it must also be which type proxy service to connect to. 

1. compile ClientProxy.
	
	go build github.com/BII-Lab/DNS-layer-Fragmentation/ClientProxy

2. if you want to redirect all you nomal DNS traffic to the proxy, configure your /etc/resolv.conf. Set nameserver to 127.0.0.1.(optional)

3. run ClientProxy, as:

	./ClientProxy -proxy "ServerProxy IP:Port"

For exaple if the server is ruing ServerPorxy on 202:104:10:10:10000, we use 
	./ClientProxy -proxy 202:104:10:10:10000

4. For more help information, you can use -h option
	
	./ClientProxy -h
Testing
-------

Make sure you have a working "dig" command. If you setup your ClientProxy and ServerProxy acorrding to the instruction, then you should be able to say:

	dig @127.0.0.1 www.yeti-dns.org aaaa

and get a result back using our fragements proxy. If you want to see the details, you can use -debug for more running information.


