# Next 

## Bug on housekeeping of expired entities

```shell
57145db4a095:/# curl --silent --digest -u "admin:$DRL_PRIVATE_API_KEY" drl-drl-2:8082/blocked-entity/ | jq 
[
  {
    "id": "f8684dd6e12af16a",
    "ip": "10.0.0.78",
    "uriPath": "api/client/list",
    "headers": {
      "x-api-consumer-id": "A6F5DCC3-AA62-4FA4-8317-8AF01A6F1028"
    },
    "expires_at": "2026-02-28T13:02:54Z"
  }
]
57145db4a095:/# curl --silent --digest -u "admin:$DRL_PRIVATE_API_KEY" drl-drl-2:8082/blocked-entity/ | jq 
[
  {
    "id": "f8684dd6e12af16a",
    "ip": "",
    "uriPath": "",
    "headers": null,
    "expires_at": "2026-02-28T13:02:54Z"
  }
]
```

it seems the entities are disappearing and after a while (probably a sync amongs nodes) they reappear again. The sinc among nodes is too aggressive also. 
Probably the sync should. 
Initial push pull should only be at the start 
