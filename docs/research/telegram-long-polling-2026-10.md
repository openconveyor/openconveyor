# Telegram Bot API long polling for an in-cluster Go bot

Resolves wayfinder ticket #99 (map #98). Researched 2026-10-31. Sources: the
Bot API reference (Bot API 10.3, August 24, 2026), the Bot FAQ, the Bot
Features page, and the webhook guide. Also the source of the open-source Bot
API server (`tdlib/telegram-bot-api`, `Client.cpp` on `master`) and the
`go.mod`, README and source of the Go libraries named below. I ran nothing
against Telegram. The text marks items that only a live bot can confirm as
**unverified**.

## Verdict

Long polling is the right fit for a bot in a kind cluster with no inbound
reachability. The bot opens outbound HTTPS to `api.telegram.org` only. The
server queues updates for 24 hours and remembers which ones the bot
confirmed. A restart without local state therefore loses nothing that is
less than a day old. The contract is at-least-once. The bot confirms a batch
only with the *next* `getUpdates` call, so a crash between "received" and
"next call" redelivers the batch. Derive the Task name from `chat.id` and `message_id`,
or keep a small seen-set of `update_id`, so a redelivery does not start a
second agent run.

Allowlist on `message.from.id` (a 64-bit integer, stable for the lifetime of
the account). Reply to `message.chat.id`. Send `allowed_updates:
["message"]` so the queue carries only what the bot handles. Run exactly one
poller: a second poller for the same token gets `409 Conflict` and both
instances miss updates. Reply in plain text, or in HTML if the reply needs
links. MarkdownV2 needs 18 characters escaped and is the common source of
`400 Bad Request: can't parse entities`. Keep replies at 4096 characters or
shorter.

The raw HTTP API is enough for this bot. It needs two methods (`getUpdates`,
`sendMessage`), both are plain JSON over `net/http`, and the long-poll loop
is about 60 lines. If you want a library, take `github.com/go-telegram/bot`.
It has no third-party dependencies, it tracks the current Bot API version
(10.3), and its poll loop does what this document recommends. Do not use `go-telegram-bot-api/telegram-bot-api`; its last
commit is from October 2022.

Egress for the bot's own NetworkPolicy: TCP 443 to `api.telegram.org`
(resolves today to `149.154.166.110`, TTL about 200 s, inside Telegram's
published `149.154.160.0/20`) plus DNS. Use the two published CIDR blocks
`149.154.160.0/20` and `91.108.4.0/22` instead of a resolved address,
because the address can change under the policy.

## Answers by question

### getUpdates and offset semantics

Source: [getUpdates](https://core.telegram.org/bots/api#getupdates),
[Getting updates](https://core.telegram.org/bots/api#getting-updates),
[Update](https://core.telegram.org/bots/api#update).

- Request: `GET` or `POST https://api.telegram.org/bot<token>/getUpdates`.
  Parameters go in the query string, as form data, or as
  `application/json`. The response is `{"ok": true, "result": [Update...]}`.
- `offset`: "Identifier of the first update to be returned. Must be greater
  by one than the highest among the identifiers of previously received
  updates. By default, updates starting with the earliest unconfirmed
  update are returned. An update is considered confirmed as soon as
  getUpdates is called with an offset higher than its update_id."
  A negative offset returns updates "starting from -offset update from the
  end of the updates queue. All previous updates will be forgotten."
- `limit`: 1-100, default 100.
- `timeout`: "Timeout in seconds for long polling. Defaults to 0, i.e.
  usual short polling. Should be positive, short polling should be used
  for testing purposes only." The reference states no upper bound. The
  server source clamps it: `get_integer_arg(query.get(), "timeout", 0, 0,
  LONG_POLL_MAX_TIMEOUT)` with `LONG_POLL_MAX_TIMEOUT = 50`
  ([Client.cpp](https://github.com/tdlib/telegram-bot-api/blob/master/telegram-bot-api/Client.cpp),
  `process_get_updates_query`; [Client.h](https://github.com/tdlib/telegram-bot-api/blob/master/telegram-bot-api/Client.h)).
  So values above 50 behave as 50. Whether the hosted `api.telegram.org`
  runs the same clamp is **unverified**; the source is the published
  server, so assume yes. Set the Go `http.Client` timeout above the poll
  timeout (for example poll 30 s, client 40 s), or the client cancels the
  poll before the server answers.
- The same source throttles tight loops: when a call repeats the same
  `offset` within 3 s with `timeout < 3`, the server raises the timeout to
  3 s; within 0.5 s it reduces `limit` to 1.
- Notes under the method: "This method will not work if an outgoing
  webhook is set up." and "In order to avoid getting duplicate updates,
  recalculate offset after each server response."
- Retention: "Incoming updates are stored on the server until the bot
  receives them either way, but they will not be kept longer than 24
  hours."
- `update_id` is sequential, but "If there are no new updates for at least
  a week, then identifier of the next update will be chosen randomly
  instead of sequentially." Do not persist an offset across long idle
  periods and treat it as a floor; start every process with `offset`
  unset (or `0`) and let the server return the earliest unconfirmed update.
- "At most one of the optional fields can be present in any given update."

Loop shape that follows from the above:

1. Start with `offset = 0`.
2. Call `getUpdates(offset, timeout=30, allowed_updates=["message"])`.
3. For each update, handle it, then set `offset = update_id + 1`.
4. Repeat. The next call confirms the previous batch.

Restart behaviour: a process that restarts without local state receives
every update that was never confirmed, up to 24 hours back. A process that
confirmed a batch and then crashed before it finished the work does not see
that batch again. For a bot that creates Tasks, create the Task for an
update *before* the next `getUpdates` call, and make the creation
idempotent. If a restart must also drop the backlog (for example after a
long outage), call `deleteWebhook` with `drop_pending_updates: true`
([deleteWebhook](https://core.telegram.org/bots/api#deletewebhook)); it is
documented to "drop all pending updates" and works when no webhook is set.

Two pollers on one token: the server keeps one open long poll per bot. A
new `getUpdates` call aborts the open one with `409 Conflict: terminated by
other getUpdates request; make sure that only one bot instance is running`,
rate limited to one 409 per 3 s (`abort_long_poll`, `fail_query_conflict`
in `Client.cpp`). With a webhook set the error is `409 Conflict: can't use
getUpdates method while webhook is active; use deleteWebhook to delete the
webhook first`. Run the bot as a Deployment with `replicas: 1` and
`strategy: Recreate`, not `RollingUpdate`, or the old and the new Pod fight
for the queue during a rollout.

### allowed_updates

Source: [getUpdates](https://core.telegram.org/bots/api#getupdates).

- "A JSON-serialized list of the update types you want your bot to
  receive. [...] Specify an empty list to receive all update types except
  chat_member, message_reaction, and message_reaction_count (default). If
  not specified, the previous setting will be used."
- The setting is sticky on the server. Send it on every call anyway; it
  costs nothing and makes the process self-describing.
- "this parameter doesn't affect updates created before the call to
  getUpdates, so unwanted updates may be received for a short period of
  time." Ignore updates whose `message` field is absent instead of failing.
- For this bot: `["message"]`. Add `"edited_message"` only if edits must
  re-trigger, which they should not for an agent run. `my_chat_member`
  reports when a user blocks the bot ("For private chats, this update is
  received only when the bot is blocked or unblocked by the user"); it is
  not needed.

### Rate limits

Source: [Bot FAQ, "My bot is hitting limits"](https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this),
[ResponseParameters](https://core.telegram.org/bots/api#responseparameters),
[Paid Broadcasts](https://core.telegram.org/bots/api#paid-broadcasts).

- "In a single chat, avoid sending more than one message per second." Short
  bursts are tolerated; repeated violations get `429` errors.
- "In a group, bots are not be able to send more than 20 messages per
  minute."
- Broadcast: "By default, all bots are able to broadcast up to 30 messages
  per second to their users."
- A `429` carries `parameters.retry_after`: "In case of exceeding flood
  control, the number of seconds left to wait before the request can be
  repeated." Honour it; the server source shows the same value in the
  description text `Too Many Requests: retry after N`.
- No documented limit on `getUpdates` call rate. The 3 s / 0.5 s throttle
  in the server source (above) is the only mechanism. A long poll with
  `timeout` 30 s makes at most two calls a minute when idle.
- An allowlisted bot that answers each command with one or two messages
  stays far below every limit. The only place to add care is the "later
  reply with PR link" path, if many Tasks finish at once: send them in
  series, not in parallel.

### Sender id and chat id for an allowlist

Source: [User](https://core.telegram.org/bots/api#user),
[Chat](https://core.telegram.org/bots/api#chat),
[Message](https://core.telegram.org/bots/api#message).

- `update.message.from.id` is the sender. `User.id`: "Unique identifier
  for this user or bot. This number may have more than 32 significant bits
  [...] But it has at most 52 significant bits, so a 64-bit integer or
  double-precision float type are safe for storing this identifier."
  Decode into `int64`. Never into `int` on 32-bit or `float32`.
- `update.message.chat.id` is where to reply. Same 52-bit note; also
  `int64`. Negative values are groups and channels (supergroup ids are
  `-100…`; the sign convention is used throughout the API but is not
  stated as a rule in the reference, so treat it as **unverified**
  folklore and switch on `chat.type` instead).
- `message.from` is "Optional. Sender of the message; may be empty for
  messages sent to channels. For backward compatibility, if the message
  was sent on behalf of a chat, the field contains a fake sender user in
  non-channel chats." So: reject when `from` is absent, and reject when
  `sender_chat` is present (anonymous group admin or linked channel),
  because `from` is then a placeholder, not the human.
- `message.from.is_bot` is `true` for bots. Bots never see other bots'
  messages anyway ("bots will not be able to see messages from other bots
  regardless of mode", FAQ), but the check costs nothing.
- Allowlist by `from.id`, not by `username`. Usernames are optional,
  mutable and reusable; the id is neither.
- A user cannot be messaged until they contact the bot. The Features page
  says "Users will see a Start button the first time they open a chat with
  your bot" and `/start` "begins the interaction". The bot cannot reach a
  user who has not started it (a `400 Bad Request: chat not found` comes
  back; string from `Client.cpp`). For the "reply later with a PR link"
  path this means the `chat.id` captured at command time is the only
  address the bot has; store it on the Task (label or annotation) at
  creation time.

### Private chats versus groups

Source: [Bot Features, Privacy Mode](https://core.telegram.org/bots/features#privacy-mode),
[Bot FAQ, "What messages will my bot get?"](https://core.telegram.org/bots/faq#what-messages-will-my-bot-get),
[Chat](https://core.telegram.org/bots/api#chat),
[Bot Features, BotFather settings](https://core.telegram.org/bots/features#botfather).

- `chat.type` is one of `private`, `group`, `supergroup`, `channel`. In a
  private chat the bot receives every message. In a group with Privacy
  Mode on (the default) the bot receives only "Commands explicitly meant
  for them" (`/command@this_bot`), replies to its own messages, service
  messages and inline results. "Bot admins and bots with privacy mode
  disabled will receive all messages except messages sent by other bots."
- Commands: "Commands must always start with the / symbol and contain up
  to 32 characters." In a group a command may carry the `@botname` suffix;
  strip it before matching. The `entities` array marks the command with
  `type: "bot_command"` and an offset/length in UTF-16 code units.
- Recommendation for this example: restrict the bot to private chats.
  Switch off group joins in BotFather: "/setjoingroups – toggle whether
  your bot can be added to groups or not." and "if your bot was not
  designed to work in groups, you can disable this." Then also enforce it
  in code: handle `chat.type == "private"` only, and ignore the rest. A
  group chat would make every allowlisted user's command visible to
  non-allowlisted members, and the bot's replies with repo links too.
- Group ids change when a group becomes a supergroup; the API reports it
  in `parameters.migrate_to_chat_id` on a failed send. Private chats do
  not migrate. One more reason to stay private-only.
- Forum supergroups need `message_thread_id` on replies; private chats do
  not (sendMessage: "for forum supergroups and private chats of bots with
  forum topic mode enabled only").

### Message length and formatting for replies

Source: [sendMessage](https://core.telegram.org/bots/api#sendmessage),
[editMessageText](https://core.telegram.org/bots/api#editmessagetext),
[Formatting options](https://core.telegram.org/bots/api#formatting-options),
[LinkPreviewOptions](https://core.telegram.org/bots/api#linkpreviewoptions).

- `sendMessage.text`: "Text of the message to be sent, 1-4096 characters
  after entities parsing". `editMessageText` has the same 1-4096 limit.
  Captions are 0-1024. Over the limit, the server answers `400 Bad
  Request: message is too long` (the server source has the check as
  `"Text is too long"`; the exact wire text is **unverified**). Characters,
  not bytes: count runes, or UTF-16 code units for entity offsets. Split a
  longer reply into several messages.
- Three ways to format: `parse_mode: "MarkdownV2"`, `parse_mode: "HTML"`,
  or an explicit `entities` array with no `parse_mode`.
- MarkdownV2 escaping, verbatim: "In all other places characters '_',
  '*', '[', ']', '(', ')', '~', '`', '>', '#', '+', '-', '=', '|', '{', '}',
  '.', '!' must be escaped with the preceding character '\'." Inside
  `code`/`pre`, escape `` ` `` and `\`. Inside the `(...)` of a link,
  escape `)` and `\`. Any user-supplied or Git-supplied string (branch
  name, PR title, issue title, error text) must go through an escaper
  first, because `-`, `.`, `_` and `#` are all common in such strings. A
  missed character fails the whole send with `400 Bad Request: can't parse
  entities`. Note that the legacy `Markdown` mode is a different, weaker
  grammar; do not use it.
- HTML mode supports `<b>`, `<i>`, `<u>`, `<s>`, `<code>`, `<pre>`,
  `<a href="...">`, `<blockquote>`, and spoilers. Only `<`, `>` and `&`
  need escaping in text, which `html.EscapeString` from the Go standard
  library does. For a bot that posts links, HTML is the lower-risk
  choice: `<a href="https://github.com/o/r/pull/7">PR #7</a>`.
- Plain text (no `parse_mode`) needs no escaping at all. Telegram clients
  still auto-link bare URLs, and a bare `https://github.com/...` URL is
  clickable. For "here is your PR" replies plain text is enough.
- Link previews: pass `link_preview_options: {"is_disabled": true}` to
  keep the GitHub preview card out of the chat.
- Replies: `reply_parameters: {"message_id": <id>}` quotes the user's
  command. Set `allow_sending_without_reply: true` so the reply still
  sends if the user deleted the command in the meantime.
- Links of the form `tg://user?id=<id>` to mention a user by id work only
  inside an inline link or button, and only if the user has talked to the
  bot; not needed here.

### The bot token

Source: [Authorizing your bot](https://core.telegram.org/bots/api#authorizing-your-bot),
[Bot Features, "Creating a new bot"](https://core.telegram.org/bots/features#creating-a-new-bot),
[getMe](https://core.telegram.org/bots/api#getme),
[logOut](https://core.telegram.org/bots/api#logout),
[File](https://core.telegram.org/bots/api#file).

- Format: `123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11`, that is
  `<bot user id>:<secret>`. It is used as a URL path segment:
  `https://api.telegram.org/bot<token>/<method>`, with the literal prefix
  `bot`. There is no header-based alternative. Consequence: the token
  appears in every request URL, so never log request URLs, and keep the
  HTTP client's error strings (which embed the URL) out of logs and out
  of messages sent back to users. Wrap errors before logging.
- File downloads use the same host and the same token in the path:
  `https://api.telegram.org/file/bot<token>/<file_path>`.
- What a leaked token can do: "Keep your token secure and store it safely,
  it can be used by anyone to control your bot." Concretely, with the
  token anyone can call `getUpdates` and read every message users send to
  the bot (and steal them from the real bot, because the queue is
  consumed once), send messages as the bot to any chat the bot has seen,
  call `setWebhook` to redirect all future updates to their server (which
  also breaks the legitimate poller with `409`), and download any file
  users sent. Through the Bot API the token cannot read the chat history
  that predates the leak (the queue holds 24 hours of unconfirmed
  updates at most), and it cannot change the bot's name, settings or
  token (those are BotFather actions by the owner account).
- Revoke: "If your existing token is compromised or you lost it for some
  reason, use the /token command to generate a new one." (BotFather.) The
  old token stops working at once. A bot that stores the token in a
  Kubernetes Secret only needs a Secret update and a Pod restart.
- Validate the token at start with `getMe` ("A simple method for testing
  your bot's authentication token"). A wrong token returns `401
  Unauthorized`. Fail fast on 401; do not retry it.
- `logOut` and `close` are for moving to a self-hosted Bot API server
  and lock the bot out of the cloud server for 10 minutes. Do not call
  them.
- Repo fit: the token is a runtime secret of the bot Deployment, not of a
  Task. Mount it as a file (same rule as ADR-0004 for Tasks) and read it
  once at start. Agents never need it; they report back to the bot
  through the Task status or an annotation, and the bot does the Telegram
  call.

### Go client library or raw HTTP

Sources: [go-telegram/bot](https://github.com/go-telegram/bot)
([go.mod](https://github.com/go-telegram/bot/blob/main/go.mod),
[get_updates.go](https://github.com/go-telegram/bot/blob/main/get_updates.go),
[bot.go](https://github.com/go-telegram/bot/blob/main/bot.go),
[releases](https://github.com/go-telegram/bot/releases));
[PaulSonOfLars/gotgbot](https://github.com/PaulSonOfLars/gotgbot)
([go.mod](https://github.com/PaulSonOfLars/gotgbot/blob/v2/go.mod),
[releases](https://github.com/PaulSonOfLars/gotgbot/releases));
[mymmrac/telego](https://github.com/mymmrac/telego);
[go-telegram-bot-api/telegram-bot-api](https://github.com/go-telegram-bot-api/telegram-bot-api)
([commits](https://github.com/go-telegram-bot-api/telegram-bot-api/commits/master)).

| Library | Bot API | Go floor | Third-party deps | State on 2026-10-31 | Notes |
|---|---|---|---|---|---|
| `github.com/go-telegram/bot` | 10.3 | `go 1.18` | none ("zero-dependencies") | v1.27.0 (11 Sep 2026), MIT | Poll loop: `offset = lastUpdateID + 1`, sends `allowed_updates` only when set, backs off 100 ms doubling to 5 s on errors, waits `retry_after` on 429, stops on `ctx.Done()`. Default poll timeout 1 min (sent as 59 s, so the 50 s server clamp applies), HTTP client timeout 1 min, `getMe` at `New()` unless skipped, 1024-slot update channel, 1 worker. Typed `TooManyRequestsError` with `RetryAfter`. |
| `github.com/PaulSonOfLars/gotgbot/v2` | 10.3 (rc.37, 17 Oct 2026) | `go 1.24` | none ("only uses standard library") | still `v2.0.0-rc.*`, MIT | Code generated from a Bot API spec, so types track the API closely. Pre-release version string in `go.mod` forever. |
| `github.com/mymmrac/telego` | 10.3 | not checked | `fasthttp`, `go-json` by default | MIT | Non-standard HTTP stack; more surface than this bot needs. |
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | not stated | not checked | none | last commit 20 Oct 2022, MIT | Effectively unmaintained for four years. Do not pick it for a new example. |

Assessment:

- The bot uses two methods: `getUpdates` and `sendMessage`. Both take a
  small JSON body and return a small JSON body. Three Go structs (`Update`,
  `Message` with `from`/`chat`/`text`/`entities`, and the response
  envelope with `ok`/`description`/`error_code`/`parameters.retry_after`)
  cover it. Unknown JSON fields are ignored by `encoding/json`, so new Bot
  API releases do not break the decoder.
- The example's purpose is to show a trigger, not a Telegram client. A raw
  `net/http` loop keeps the example readable top to bottom and adds no
  module to `go.mod`. This matches how the other examples in `examples/`
  stay dependency-free (ConfigMap + CronJob, webhook YAML).
- If a library is chosen anyway, `go-telegram/bot` is the one: no
  transitive dependencies, current API version, active releases, and its
  loop already does the offset, backoff and `retry_after` handling listed
  above. It is also the only one that lets the example skip writing the
  loop at all (`bot.New(token, bot.WithAllowedUpdates([]string{"message"}))`,
  `RegisterHandler`, `b.Start(ctx)`).
- Either way, write the loop (or configure the library) with: poll
  `timeout` 30-50 s, HTTP client timeout at least 10 s longer, exponential
  backoff with a cap on transport errors, immediate exit on `401`, log and
  continue on `409` (another instance is alive; do not spin), sleep
  `retry_after` on `429`.

### Hosts the bot must reach (egress)

Sources: [Making requests](https://core.telegram.org/bots/api#making-requests),
[Bot webhooks guide](https://core.telegram.org/bots/webhooks),
`dig api.telegram.org` on 2026-10-31.

- Exactly one host: `api.telegram.org`, TCP 443, HTTPS only ("All queries
  to the Telegram Bot API must be served over HTTPS"). File downloads use
  the same host (`/file/bot<token>/...`). No other Telegram host is
  involved in Bot API traffic.
- Resolution on 2026-10-31: A `149.154.166.110` (TTL 204 s at query time),
  AAAA `2001:67c:4e8:f004::9`. The A record lies inside
  `149.154.160.0/20`, one of the two subnets Telegram publishes in the
  webhook guide ("subnets 149.154.160.0/20 and 91.108.4.0/22"). The guide
  publishes them as *source* ranges for webhook calls and warns "Our
  IP-range might change in the future"; that the API front end also lives
  in them is an observation, not a documented promise.
- For the bot's own NetworkPolicy, allow egress to `149.154.160.0/20`
  and `91.108.4.0/22` on TCP 443, plus UDP/TCP 53 to kube-dns. A rule
  that pins the single resolved address breaks the moment Telegram moves
  the A record, and the TTL of about 200 s says they can. If the cluster
  routes IPv6, add Telegram's `2001:67c:4e8::/48` (the AAAA above is
  inside it; the prefix is from the AS62041 allocation, **unverified**
  against any Telegram document) or disable IPv6 in the Pod. The Task
  controller's `spec.permissions.egress` already accepts CIDR blocks for
  this reason (`api/v1alpha1/task_types.go`), so the same list can go on a
  Task if an agent ever needs to call Telegram itself.
- The bot also talks to the Kubernetes API server (to create Tasks and
  watch their status). That is in-cluster traffic to the `kubernetes`
  Service; allow it separately.
- Nothing inbound. Telegram never connects to a long-polling bot.

## Open points for the implementer

- Idempotency key: name the Task `tg-<chat_id>-<message_id>` or keep the
  last processed `update_id` in a ConfigMap. The first needs no state and
  survives redelivery; the second also survives a replay from
  `offset = 0` after a week-long gap when ids restart at a random number.
- Reply-later path: the bot needs the `chat.id` (and ideally the
  `message_id` to reply to) when the Task finishes. Store both as labels
  or annotations on the Task at creation and read them back from the Task
  on the status transition. Prefer annotations: a label value must start
  and end with an alphanumeric character, and a group or channel `chat.id`
  starts with `-`. Private chat ids are positive and fit a label.
- Whether the hosted server clamps `timeout` at 50 s like the open-source
  server, and the exact `400` texts for too-long and unparsable messages,
  can be confirmed in five minutes with a real token and `curl`. Both are
  marked **unverified** above.
