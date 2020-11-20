#!/bin/bash

export VAULT_CACERT="dev_utils/ca.crt"
export VAULT_ADDR="https://127.0.0.1:8200"

for v in V1 V2
do docker exec $v vault operator init -n 1 -t 1 > $v.init
done

for v in V1 V2
do docker exec $v vault operator unseal "$(grep Unseal $v.init | cut -d ' ' -f4)"
done

until docker exec V1 vault status | grep active;
do sleep 1;
done

V1_TOKEN=$(vault login -field=token "$(grep Root V1.init | cut -d ' ' -f4)")

VAULT_ADDR="https://127.0.0.1:8200" VAULT_TOKEN=$V1_TOKEN vault policy write snapshotter -<< EOH
path "/sys/storage/raft/snapshot"
{
  capabilities = ["read"]
}
path "auth/approle/login" {
  capabilities = [ "create", "read" ]
}
EOH

VAULT_TOKEN=$V1_TOKEN vault auth enable approle

VAULT_TOKEN=$V1_TOKEN vault write auth/approle/role/snapshotter token_policies=snapshotter

VAULT_TOKEN=$V1_TOKEN vault secrets enable kv

VAULT_TOKEN=$V1_TOKEN vault kv put kv/my-secret my-value=s3cr3t
