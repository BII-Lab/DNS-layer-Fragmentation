This code implement proxies for DNS application-level fragmentation,
based on an idea originally submitted on the IETF DNSOP mailing list
by Mukund Sivaraman <muks@isc.org>:

  https://www.ietf.org/mail-archive/web/dnsop/current/msg12964.html

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
