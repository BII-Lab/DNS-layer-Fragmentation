This code implement proxies for DNS application-level fragmentation,
based on an IETF draft:

http://www.ietf.org/id/draft-muks-dns-message-fragments-00.txt

In IPv4, datagrams that do not fit into a single physical packet are
sometimes split into several smaller packets and reassembled by the
network. The idea with these proxies is to investigate the idea of
splitting DNS messages in the protocol itself, so they will not by
fragmented by the IP layer.

This is a proof of concept setup, which provides a client proxy and a
server proxy. The data flow works like this:

* Query:
  * DNS packets arrive at the client proxy, which listens on the
    well-known port 53.
  * The client proxy then sends the DNS packets over the network to a
    custom port on the server proxy.
  * The packets arrive at the server proxy, which then sends them to
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

To compile the code, make sure your have install golang 1.4 version and  already compiled go dns lib written by miekg(https://github.com/miekg/dns).

go get github.com/BII-Lab/DNS-layer-Fragmentation
go build github.com/BII-Lab/DNSoverHTTPinGO/ClientProxy
go build github.com/BII-Lab/DNSoverHTTPinGO/ServerProxy

Server Installation
-------------------

The server proxy will need a working name server configuration on your server. The servershould be reachable by UDP and TCP, and you should have a clear ICMP path toit, as well as full MTU (1500 octets or larger) and the ability to receive. And the server proxy need be assigned a port to listen on as the port for this proxy.
fragmented UDP (to make EDNS0 usable.)

1.compile ServerProxy.
2.make sure you have a working resovler.
3.run the ServerProxy as ./ServerProxy -proxy "[your resovler ip address]" -listen ":[your assigned port]". For exmaple ./ServerProxy -proxy "127.0.0.1:53" -listen ":10000"

Client Installation
-------------------

The ClientProxy will listen on the port assigned(defort port is 53). And it must also be which type proxy service to connect to. 

1. compile ClientProxy.
2. if you want to redirect all you nomal DNS traffic to the proxy, configure your /etc/resolv.conf. Set nameserver to 127.0.0.1.(optional)
3.run ClientProxy. Example ./ClientProxy -proxy="192.168.37.121:10000" -listen ":53"

Testing
-------

Make sure you have a working "dig" command. If you started your client side
dns_proxy service on 127.0.0.1, then you should be able to say:

	dig @127.0.0.1 www.baidu.com a

and get a result back. If you want to see the details, you can use -debug for more running information.

