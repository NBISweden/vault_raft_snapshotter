disable_mlock = 1
ui = 1
log_level = "Debug"

listener "tcp" {
  address = "0.0.0.0:8200"
  tls_cert_file = "/vault/config/server.crt"
  tls_key_file  = "/vault/config/server.key"
  tls_ca_file = "/vault/config/ca.crt"
}


storage "raft" {
  path = "/tmp"
  node_id = "node1"
}

cluster_addr = "https://127.0.0.1:8201"
