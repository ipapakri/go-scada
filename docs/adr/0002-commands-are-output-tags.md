# Commands are output tags, stored but never replayed

A command to a device, such as starting a motor, is a value published to an ordinary tag whose binding has the output direction, for example `factory1.motor1.commands.start`. There is no separate command subject, no request-reply and no command log. `VAL` keeps the last value of an output tag like any other value. That gives a trace of the last command and its source (instance, plus the user when a person wrote it). The archive service can export the history of chosen output tags as an audit report.

The driver subscribes to each output tag live and writes every new value it receives, including a repeat of the same value. It never writes the value restored from `VAL` at startup, so a restart doesn't replay the last command. It drops only exact `message_id` duplicates and doesn't order by `observed_at`, because two writers on hosts with skewed clocks would otherwise lose real commands. A failed write is published back on the same tag with quality `BAD` and reason `WRITE_FAILED`, with the driver instance as the source. The driver ignores messages whose source is itself.

## Considered options

- **Request-reply on `_scada.<site>.cmd.write.<tag path>`, with an `EVENTS` audit stream**, as #15 first proposed. We rejected it because it needs a second namespace, a second store and a second permission model for something a tag already expresses.

## Consequences

- A binding is either input or output. Some devices read and write the same register, and they may later need an input/output direction. Until then, model them as two tags.
