# enserie

P2P overlay network: QUIC first, relay as fallback.

Two peers get addresses from a shared IPv4 `/30` and reach each other as if
they were on a LAN. Direct QUIC is preferred. When NAT blocks it, a blind
relay splices the path. Session bytes stay inside TLS.

This repository is a Go module. tyd is unchanged and does not import it yet.
