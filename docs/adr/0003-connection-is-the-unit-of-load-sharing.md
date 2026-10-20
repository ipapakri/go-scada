# A connection is the unit of load sharing

Driver instances share work by connection: each connection in `cfg_connections` names the instance that polls it, and binding keys in `cfg_bindings` start with `<site>.<driver>.<connection>.`. An instance watches only the bindings of its own connections. Moving a connection to another instance means rewriting its `instance` field, and no binding data moves. A binding key is the only record of which connection feeds a tag. Tags carry no `source` field, and a tag can have at most one binding.

Bindings also carry everything the driver needs to publish: register, encoding, direction, deadband and scaling. The encoding sets the value kind. So a driver never watches `cfg_tags`, a bucket that isn't partitioned by connection. The designer and `scada-cli project apply` reject a binding whose encoding doesn't match its tag's type.

## Considered options

- **Sharing by tag**, which would balance load more finely. We rejected it because a Modbus connection is one TCP link that polls registers in groups, so splitting its tags across instances would open several links to one device.
- **A `source` field on the tag** that duplicates the binding key. We rejected it because KV has no transactions, so the two records could disagree after a crash.
