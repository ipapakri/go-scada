# Site-first namespace: only values on the bus, everything else in KV

Every NATS server runs one site, and its JetStream domain is the site name. The only traffic under `<site>.>` is values, at `<site>.<tag path>`. That covers input tags, output tags, memory tags and the service-owned `_sys` status tags. Everything else, meaning configuration, alarm state and alarm acknowledgements, lives in JetStream KV buckets with plain names (`cfg_tags`, `alarms`, `alarm_acks`, ...) and keys that start with `<site>.`. Services refuse to start when the JetStream domain differs from the configured site.

We put the site first so that an overview system can bridge several sites and merge their streams and buckets by sourcing them. The keys and subjects already carry the site, so nothing collides. Keeping only values under `<site>.>` lets the `VAL` stream capture the whole prefix: a JetStream stream can't exclude subjects, so any command, event or request under the prefix would be stored too, and JetStream would answer requests with a publish ack.

## Considered options

- **A reserved `_scada.<site>.cmd.>` / `.evt.>` root**, as #15 first proposed. We rejected it because it breaks "the site comes first" for bridging, and because commands became output tags (ADR-0002).
- **A kind token after the site** (`<site>.val.…`, `<site>.cmd.…`). We rejected it because nothing outside values needs a subject once acknowledgements are KV puts.
- **The site in bucket names** (`factory1_cfg_tags`). We rejected it because site-prefixed keys already prevent collisions when a hub merges buckets.

## Consequences

- An alarm acknowledgement is a fire-and-forget `kv.Put` on `alarm_acks`. If it is lost, the alarm stays pending and the operator acknowledges again.
- There is no `EVENTS` stream. Alarm history comes from KV revisions of `alarms` and `alarm_acks`, which the archive service (#2) reads.
- Tag path tokens carry no meaning, so NATS permissions can't tell an output tag from an input tag. The designer gateway checks every browser write.
