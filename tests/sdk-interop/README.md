# Generated Go / SDK interoperability

For a sequential local functional acceptance run (existing toolchains/dependencies,
macOS with Xcode for the Swift iOS compilation check):

```sh
bash tests/sdk-interop/acceptance.sh all
```

In a standalone core checkout, first run `sh scripts/setup-modules.sh .` once
to bind the unpublished candidate modules. Generated fixtures import the PostgreSQL
adapter even when they do not query a database. Keep workspace mode enabled; an
existing workspace must explicitly bind this checkout and its adapter modules.
The setup command refuses to overwrite an existing `go.work`.

Use `ts`, `dart`, `kotlin` or `swift` to select one SDK. The runner stops at the first
failure and retains per-stage logs plus `results.tsv` under `.tmp/sdk-acceptance.*`.
This is not a performance benchmark, release approval, or application build wrapper.

The generated-consumer fixture compiles `schema.luxo` with the real lexer, parser, semantic analyzer,
lock-ID allocator and Go generator. The generated handlers, schema, row/column
writers and stream encoders run behind the real Luxo router. Unary APIs contain
compiled Luxo bodies; `@native` supplies stream values and a preauthenticated test
identity for token lifecycle assertions. No database, mock
HTTP responses, handwritten payload codec or new dependency is required.

The Dart, TypeScript, Kotlin and Swift suites fetch live introspection and generate/type-check
their clients. Dart runs HTTP/WebSocket, JSON/Binary assertions on VM, JavaScript
and Wasm; TypeScript 7 compiles and executes CJS/ESM consumers on Node. Kotlin
compiles an independent JVM consumer without the serialization compiler plugin.
Swift compiles an independent SwiftPM consumer with Swift 6 strict concurrency
and warnings as errors, using only the SDK's public runtime product.
Both Swift build stages require the checked-in `swift/Package.resolved`; the
generated consumer receives an unchanged copy. An incompatible or missing lock
fails the build instead of updating dependencies. The local SDK source revision
still needs separate release pinning; a dependency lock does not pin that source.
Values come from the shared Go protocol fixture, including both Int64 extremes
and values beyond JavaScript's exact-number range. It tests scalar Int/Duration,
integer arrays, structured inputs, nullable values, columnar results, partial
selection and native streams. Structured errors and in-memory model pagination
also use real compiled handlers. All four SDKs additionally exercise a generated
gateway with a separate TCP RPC backend as described below. That four-SDK fixture
does not exercise extension-response merging, multiple backend modules or device
consumers; the separate Go gateway fixture below covers the first two.

## PostgreSQL selection fixture

From the core repository, generate and build a fresh fixture without reusing prior generated artifacts:

```sh
mkdir -p .tmp
fixture=$(mktemp -d "$PWD/.tmp/sdk-sql.XXXXXX")
go run ./tests/sdk-interop -sql -out "$fixture"
go build -o "$fixture/server" "$fixture"
LUXO_TEST_DATABASE_URL="$DEDICATED_TEST_DATABASE_URL" "$fixture/server"
```

Use a dedicated, disposable PostgreSQL database. This fixture creates `products`
and inserts 128 rows, including IDs above JavaScript's safe integer range and
4 KiB descriptions. It refuses a missing database URL and does not drop existing
tables. Do not point it at a development or production database.

The first stdout JSON line contains `url` (TLS H2), `plain` (Schema discovery),
and the fixture certificate. Generate SDK clients using the discovery endpoint
and introspection key `benchmark`; trust only that certificate for the test.
Keep stdin open while the consumer runs. Closing stdin stops the fixture and
prints the H2/other request counts and captured SQL. The caller owns database
and scratch cleanup. Neither this server nor its credentials are for deployment.

`products` reads all database columns; `selectedProducts` applies the explicit
selection to SQL; inferred `listProducts` applies projection and pagination.
Consumers should verify selected/unselected fields, exact IDs, two 64-row pages,
and the captured SQL. These are correctness checks, not performance measurements.
`sql/runner.py` additionally owns a disposable database container and validates
the final transport/SQL report. It accepts only an already installed image
(`--pull=never`), resolves its immutable image ID, builds the server from current
source, and preserves evidence under the core `.tmp`:

```sh
python3 -B tests/sdk-interop/sql/runner.py --image <installed-pg-image> -- <consumer-command>
```

The consumer receives `LUXO_SQL_ENDPOINT`, `LUXO_SQL_SCHEMA_ENDPOINT`,
`LUXO_SQL_CERTIFICATE`, `LUXO_SQL_SCRATCH`, and `LUXO_INTROSPECTION_KEY`.
It must generate/build its SDK client and execute the five calls in the retained
`sql/consumer.mjs`, `sql/consumer.dart`, `sql/Consumer.kt`, or `sql/Consumer.swift`
assertion source. Use `--runs 2` when running both TS module formats. Failed
consumers fail the run; a successful consumer still fails if SQL or protocol
counts differ. The runner closes its server and removes only its own container.
Platform build drivers generate fresh consumers from the running Schema:

```sh
python3 -B tests/sdk-interop/sql/runner.py --image "$PG_TEST_IMAGE" --runs 2 -- python3 -B tests/sdk-interop/sql/ts.py
python3 -B tests/sdk-interop/sql/runner.py --image "$PG_TEST_IMAGE" -- python3 -B tests/sdk-interop/sql/dart.py
python3 -B tests/sdk-interop/sql/runner.py --image "$PG_TEST_IMAGE" -- python3 -B tests/sdk-interop/sql/kotlin.py
python3 -B tests/sdk-interop/sql/runner.py --image "$PG_TEST_IMAGE" -- python3 -B tests/sdk-interop/sql/swift.py
```

Prepare the repository's pinned toolchains and existing package dependencies
before running. No driver installs dependencies: Kotlin uses Gradle offline;
TS builds the local runtime and generator; Dart requires its package config;
Swift compiles the local SDK sources directly. `DART` may name the installed Dart
executable, and `LUXO_SWIFT_PATH` may override the sibling Swift repository path.
Swift runs on macOS and injects a session using the test-only initializer to trust
the fixture certificate. These commands do not exercise Android/iOS devices.
Each driver retains source/artifact hashes and generated consumers in the evidence
directory. Dart removes its owned package scratch after execution. Keep these
builds separate from timed benchmarks.

The owner terminates the consumer's process group, including descendants left
behind by an exited parent. It waits up to five seconds for graceful termination
before killing remaining group members; a regression test covers a child that
ignores SIGTERM. Processes that deliberately create another session are outside
this ownership mechanism.

### Same-output SQL projection comparison

From the core repository, run a current SDK driver with the opt-in comparison:

```sh
python3 -B tests/sdk-interop/sql/runner.py --image "$PG_TEST_IMAGE" --runs 2 --projection-benchmark -- python3 -B tests/sdk-interop/sql/ts.py
python3 -B tests/sdk-interop/sql/runner.py --image "$PG_TEST_IMAGE" --projection-benchmark -- python3 -B tests/sdk-interop/sql/dart.py
python3 -B tests/sdk-interop/sql/runner.py --image "$PG_TEST_IMAGE" --projection-benchmark -- python3 -B tests/sdk-interop/sql/kotlin.py
python3 -B tests/sdk-interop/sql/runner.py --image "$PG_TEST_IMAGE" --projection-benchmark -- python3 -B tests/sdk-interop/sql/swift.py
```

Both paths return only the same 128 names. `products` reads all database columns;
`selectedProducts` reads `name, id`. Each module format warms both paths 20 times,
then runs three alternating batches of 50 calls per path. Assertions run outside
the timed call and check every name and the absence of unselected fields.
The owner checks exactly 340 H2 calls and matching SQL counts per consumer
(680 across TS CJS/ESM). Dart uses AOT, Kotlin uses JVM/OkHttp, and Swift uses
an optimized macOS build with URLSession.
Raw JSON/CSV samples and artifact hashes stay in the evidence folder.
Each SQL SDK driver also writes `<sdk>-inputs.tar.gz` before temporary cleanup,
plus `<sdk>-inputs.json` with the archive digest and external dependency paths.
Project-local inputs are retained in the archive; external dependencies are
hash-only and are not copied from toolchain caches. This is not a standalone
installation bundle. Missing explicit inputs fail collection rather than being
silently omitted. Existing older hash-only reports are not retroactively sealed.
Compare each runtime against itself; do not rank runtimes from sequential runs.
CSV durations are in microseconds; use the nearest-rank percentile convention
when comparing them with the JSON summaries.

This is a local, single-concurrency SDK/TLS H2/PostgreSQL comparison using the
same generated client and service. The database has a one-CPU/512MiB limit; the
host client/server are not resource-isolated. It does not compare frameworks,
measure cumulative allocation or establish stable P99 from 50-call batches.
The normal correctness runner remains unchanged unless the option is supplied.


## HTTP access / refresh lifecycle

All four generated SDK consumers exercise JSON and Binary token refresh against
both the direct service and generated gateway. The fixture uses the production
`luvia.New()` identity/RPC verification setup and HTTP auth middleware. Test-owned
server processes explicitly set their own JWT configuration; developer secrets
and refresh settings must not affect these tests.

`issueFixtureAccess` and `issueFixtureRefresh` operate on a fixed preauthenticated
identity, `9007199254740993`, supplied by a native fixture. They are **not login
APIs** and must never be copied into a production login implementation. The
generated methods perform actual signing, refresh-purpose verification and access
issuance; no token or HTTP response is fabricated by the SDK consumer.

The assertions reject an access token at the refresh endpoint and a refresh token
as ordinary API access. An HTTP401 triggers a callback using a separate generated
client to exchange the refresh token; the original call succeeds with the exact
Int64 identity, and the next call does not refresh again. A callback returning
no token preserves Unauthorized; returning another refresh token fails after one
retry rather than looping. The unauthorized trigger deliberately uses the wrong
token purpose, not a wall-clock sleep; this does not replace expiry-boundary tests.

These are correctness tests, not refresh performance measurements, concurrent
single-flight guarantees, rotation/revocation coverage or mobile-device tests.

## Generated gateway / TCP RPC

### HTTP gateway performance fixture

`go test ./tests/sdk-interop -run '^TestGeneratedGatewayHTTP$' -count=1`
validates the complete HTTP gateway entry and actual TCP RPC with the generated
proxy. The fixture checks status, content type and exact response bytes on every
call, including Int64.max, one record / 128 records, no extensions / two distinct
backend services, JSON / Binary, and 1 / 16 / 128 selected scalar fields. The primary
backend verifies its exact field mask before returning a response. Negative
controls reject wrong status, mode, values, unselected extra fields and an
incorrect backend projection. List extensions are non-empty and checked against
independently assembled expected payloads.

Set `LUXO_PROXY_HTTP_BENCHMARK=1` for the opt-in serial benchmark. Connections
are warmed with 32 checked requests per case; `BenchmarkProxyHTTP` covers the
single-scalar-field control and `BenchmarkProxyHTTPWide` covers 16 / 128 fields.
Request construction, HTTP client,
router parsing, selection, RPC, merging and response read/verification are timed.
All test-owned listeners and connections are closed. Allocation counts include
both ends in the same Go process, not just the gateway. Deterministic handwritten
backend payload handlers isolate transport work; this benchmark does not include
the generated business model handlers, a database, four external SDKs, HTTP/2,
concurrent throughput or latency percentiles. It is not a gRPC comparison or a
complete protocol-freeze performance gate.

The same fixture also checks HTTP/2 h2c using the gateway's existing `h2c`
handler and an explicit HTTP/2 client. Every response must have `ProtoMajor == 2`;
a negative control rejects an HTTP/1.1 response even when its status, media type
and payload are correct. The default race test includes all 24 h2c shapes.
Set `LUXO_PROXY_HTTP2_BENCHMARK=1` for `BenchmarkProxyHTTP2`, with the same
32-call warmup, 300ms per case and exact payload checks. Run HTTP/1 and HTTP/2
benchmark switches separately. These are serial loopback HTTP/2-to-TCP-RPC
measurements, not TLS, multiplexed concurrent load, external SDKs, database work,
latency percentiles or an equivalent-work gRPC comparison.

Warm benchmarks now count successful `Accept` calls for the HTTP listener and
each RPC target. They require exactly one connection per listener both after
warmup and after timing, so reconnection cannot silently enter a reuse result.
The listener returns the original TCP connection unchanged; counting only occurs
when accepting a connection, not for each request. Regressions cover unused
counters, forced HTTP reconnection and both protocols with two RPC extensions.

For an opt-in diagnostic of the 128-row, 128-field JSON response with two
extensions, run `LUXO_PROXY_HTTP_PROFILE=1 go test ./tests/sdk-interop -run
'^TestGeneratedGatewayHTTPProfile$' -count=1 -v`. It profiles HTTP/1.1 and h2c
sequentially, prints CPU and sampled allocation summaries, then removes its
test executable and raw profiles with the owned scratch directory. Profiles
include setup and warmup; their sampled byte totals are not per-request
allocation counts and profiled timings are not performance acceptance results.

TS 7 CJS/ESM, Dart VM/JavaScript/Wasm, Kotlin JVM and Swift all run direct and
gateway deployments. The gateway tests
reuse all successful scalar, nested, columnar, pagination, nullable-array and
native-stream assertions over HTTP/WebSocket × JSON/Binary. Raw JSON number
tokens and Float sign bits are checked without converting them to approximate
numbers. The proxy functions are extracted unchanged from the real gateway
generator; only startup is supplied by the fixture. Gateway and backend run in
separate processes, with independent ephemeral listeners. The gateway registers
schema and proxies, never local business handlers. Go regressions also stop the
backend, require gateway requests to fail and verify owned processes/listeners
are closed on success, consumer failure and failed readiness.

`QueryProbe` also exports its actual CRUD query capabilities. All four consumers
compile generated `LuxoQuery_listQueryProbes` helpers, preserve an Int64 value
above JavaScript's safe integer range, and send a typed DateTime filter containing
an invalid date. The latter must still receive the server's BadRequest response;
typed construction does not bypass semantic validation. Local sorter assertions
do not prove database sorting or successful query execution.

`QueryProbe` is a generated CRUD model with no database configured. All four
consumers send invalid filters (unknown/unexposed field, undeclared enum,
enum ordering, malformed Boolean/Float/Int/DateTime) and invalid sort fields.
Each request must return `BadRequest`/400 with the offending `data.param`, then
an interleaved `echoInteger` must succeed on the same client. These assertions
run over HTTP/WebSocket × JSON/Binary in both deployments; they verify rejection
before database access, not successful SQL execution or database behavior.
Internal `svc:` RPCs are excluded from public SDK methods and transport tables;
the Swift generator has a separate regression for this compilation boundary.

This revealed a handler-metadata bug: named enum parameters were emitted as
`Model` even though gateway introspection described them as `Enum`. Service
registration now uses the compiler's enum set, preserving the declared name,
list and nullable flags. Both generated sides must agree; the fixture must not
overwrite handler metadata with schema metadata to hide disagreement.
Existing generated service handlers must be regenerated; upgrading the runtime
alone does not rewrite their static parameter tables.

The previously reproduced `Missing error data` failure is fixed for unary RPC.
Both deployments now run the same error assertions: original JSON number tokens,
full-width Int/Duration, metadata and the following successful request. Dart runs
231 direct and 231 gateway tests across VM/JavaScript/Wasm; Kotlin and Swift also
verify errors in every HTTP/WebSocket × JSON/Binary channel. Tests do not skip
gateway error assertions. The owner verifies gateway shutdown after consumer
failure and rejects cleanup of the root, siblings or symlinks. Native stream
termination errors, devices, full protocol acceptance and performance gates
remain separate; these passing results do not freeze the protocol. The Go runner
sets `LUXO_INTEROP_TOPOLOGY` from the actual deployment, replacing inherited
values; consumers reject a missing or unknown topology rather than guessing.
CI has separate direct and gateway steps, with a regression checking that
neither topology is omitted or allowed to ignore failure. Local execution does
not prove that remote CI or a separately published Swift revision has passed.

`rejectPrecision` checks the full-width integer and Unicode data in an actual
422 error, together with its name, message, trace ID and production cause policy.
TS/Dart interleave a success with each rejection to verify correlation.
`browsePrecision` checks the custom default page size, selected model fields,
zero/negative/oversized page sizes, page normalization and page numbers up to
Int64.max. Its three records are built by Luxo code, so these tests do not prove
database LIMIT/OFFSET behavior or totals near Int64.max. `checkListEffects` uses
compiled local functions to prove variable/function/API-return list elements
are evaluated once; model values, aliases and pointer-returning functions must
agree with the generated function/list ABI. No handwritten Go unary resolver
replaces this regression.

`NullableArrays` also exercises nullable Int/Duration/Boolean arrays: null, empty,
populated with signed extremes and unselected fields in both single/columnar
responses. A trailing scalar checks cursor alignment. The three value states are
also emitted by generated native streams. `NestedPrecision` adds three levels of
objects and object lists, nullable children/lists, Unicode, Int64 extremes and
partial selection. The same cases pass through row, columnar and native stream
responses. Inputs keep their separate inline nested layout.

`watchPrecision(value: Int = 42)` additionally verifies omitted native-stream
parameters through the generated clients. Each SDK subscribes without `value`
and checks the selected response fields against 42; the existing explicit-value
cases still include zero, negative values and both Int64 extremes. Introspection
exports only `hasDefault`, not the literal, so SDKs must leave the parameter absent
and let the service prepare its typed snapshot. The cases run in both WebSocket
modes through direct and gateway/TCP deployments. They do not certify every
default expression, nullable omission semantics, authorization or ACK ordering.

`ScalarArrays` covers all eleven scalar families (Int, Float, String, Boolean,
DateTime, Duration, UUID, Bytes, Decimal, JSON and enum) as nullable arrays in
single rows, columnar lists and native streams. It verifies null/empty/populated
states and unselected fields. Generic JSON includes an exact Int64 maximum;
Bytes includes empty data and high-bit bytes. DateTime compares Go's canonical
RFC3339Nano strings, preserving the fraction rather than adding a fixed `.000` suffix.
These cases do not prove arbitrary-precision generic JSON, all scalar error
boundaries or pagination against a database.

The 11 shared `uuidCanonicalCases` cover valid upper/lowercase and malformed
36-character UUIDs. Go and all four SDK unit suites consume these cases. The
generated TS/Kotlin consumers additionally call `echoUUIDs` over HTTP/WebSocket,
JSON/Binary and direct/gateway deployments, including success after rejection.
In Binary mode invalid text is rejected locally before sending; in JSON mode
the service rejects it. This does not certify arbitrary UUID input aliases,
UUID version/variant constraints or malformed JSON response domain validation.

Separately, the 20 shared `jsonNumberTokens` test both root numbers and nested
generic JSON, including JSON arrays. Cases include 256-digit integers/fractions,
exponents beyond signed 32-bit and 64-bit ranges, exponent-leading zeros,
trailing fractional zeros and negative zero. They must remain original number
text, not be converted to business Float/Decimal values. All four SDKs check these tokens over all four live
Go channels. `rejectJSON` additionally returns every token through compiled Luxo
error data, checking error identity/metadata and a following successful call;
TS/Dart also interleave the success and rejection. `watchJSON/watchJSONs` preserve
the same root and nested tokens through native scalar/list streams in both WebSocket
modes. Lists include a JSON null element. Dart emits `List<Object?>` for `[JSON]`;
its generated input, row, columnar and API readers also have null-element
regressions. This does not relax typed Int/Float ranges or outer-list nullability.
Additional `ScalarArrays` state 3 checks negative zero,
positive/negative minimum subnormals and the maximum finite value in row,
columnar and native stream replies. Comparisons include sign bits, so `0 == -0`
cannot hide a failure. TS and Dart additionally send host-language negative zero
through opaque JSON, not only pre-parsed raw tokens. Earlier native stream states
remain unchanged. Kotlin's
external consumer also injects invalid typed scalar/list/stream values and numeric
model/page fields locally. Those rejection tests do not claim the Go service sent
malformed frames or that every numeric transport path has been verified.

`scaleFloat` and `scaleFloatList` exercise actual compiled multiplication with
finite inputs. Multiplying the maximum positive/negative value by two must return
an Internal/500 error, never infinity, null, or the first item of a partial list.
All four SDK consumers check both APIs through direct and gateway deployments,
HTTP/WebSocket × JSON/Binary, then issue a valid request on the same client.
Multiplication by one preserves the bits of signed zero, minimum subnormals and
maximum finite values. These are no-data encoding errors; structured RPC data
uses the separate error assertions described above.

The fixture explicitly disables development mode and retains a separate
introspection key. The overflow regressions exposed that it previously enabled
development causes despite describing production redaction. No production
runtime was relaxed: the consumers still require no cause and no partial data.

`echoTimestamp` / `echoTimestamps` add 14 shared DateTime cases with fixed Unix
seconds, nanoseconds and canonical UTC strings, independently checked by Go's time parser.
Every SDK sends them as scalars, lists and structured input arrays, then checks
row/columnar responses through both deployments and all four channels. Cases
cover pre-epoch fractional seconds, timezone offsets, the 1582 calendar reform,
years 0000/0001 and the four-digit upper year. The regression exposed Dart's
truncation toward zero and Swift's fractional parsing and calendar-cutover bugs.
All clients and services must be regenerated: DateTime now uses signed floor
Unix seconds followed by unsigned nanoseconds (0..999999999). Internal RPC is
version 3; no whole-second fallback exists. JSON uses timezone-bearing RFC3339,
at most nine fractional digits and UTC years 0000..9999. All four runtimes also
consume 34 shared text-boundary cases; Go tests send these through direct and
gateway HTTP and WebSocket paths. Each invalid input is followed by a valid
nanosecond request on the same WebSocket connection. Fractions beyond nine digits, invalid dates, missing zones
and UTC range overflow are errors, not silently normalized values. These checks
do not certify database storage precision, device SDKs or performance.

`watchTimestamp` / `watchTimestamps` also pass the 14 timestamps through native
stream input and generated stream output. All four SDK consumers check scalar
subscriptions, empty/single/full lists and a subsequent unary request after
cancellation, in JSON/Binary through both direct and gateway deployments.
TS CJS/ESM, Dart VM/JS/Wasm, Kotlin JVM and Swift provide 476 additional DateTime
subscription scenarios. These tests do not certify stream parameter defaults,
unknown enum rejection or bounded recursive event snapshots.

The shared `jsonNumberConversions` cases define explicit signed-Int64 results
and fixed 16-digit hexadecimal Float bit patterns. Null expectations mean that
conversion must fail, not that the original numeric token is invalid. The
40 cases cover rounding ties, signed zero, subnormal/normal boundaries, overflow
and nonzero underflow. SDK tests compare against these fixed results and verify
that conversion does not change the original token. Dart also runs them locally
inside this generated consumer on VM/JS/Wasm; that is not a claim that every
conversion case traverses a network. The other SDKs consume the same corpus in
their unit suites. Swift's existing `LUXO_PROTOCOL_FIXTURE` override uses the core
file directly in CI; that path also compares the bundled fixture byte-for-byte
with the core file, so missed fixture updates fail instead of silently diverging.
Swift's shared Unicode regression consumes all 23 cases from either source,
including invalid escapes in keys and overwritten values. Local bundled tests
alone do not certify agreement with a separately published core revision.

`TestFixtureRawJSONNumericCorpus` also sends all 40 original numeric tokens
directly to generated Go handlers, before any SDK host-number conversion. Nine
Int/Duration/Float scalar, list and structured response shapes run over JSON
HTTP/WebSocket in both deployments: 1,440 numeric cases plus 768 successful calls
after rejected inputs. WebSocket reuses the same connection and checks each ID.
Selected fields, result counts, exact integers and Float bits are asserted. This
tests the real network and internal RPC conversion, not four SDKs sending every
token, database arithmetic, devices or performance.

`echoFixtureState` / `echoFixtureStates` return a named enum as a scalar or list
from compiled Luxo bodies. The corresponding native streams use the same Go
generated encoders. All four SDK consumers check both declared enum values,
empty/populated lists and WebSocket streams without `$select`. This verifies
named-enum schema resolution, not the policy for unknown future enum values.

Dart and Kotlin consumers now use explicit Known/Unknown enum types. Their
scalar/list calls also send undeclared `FUTURE`, empty and wrong-case values and
require the current Go server to return 400 in both transports and topologies.
Unknown response preservation is separately exercised by generated local
consumers. TS and Dart additionally build two real Go service revisions: the
old schema declares OPEN/CLOSED, while `-out <directory> -future-enum` compiles
a new server declaring FUTURE and emitting it in scalar/list streams. Generate
the client from the old service, stop it, then use that unchanged client against
the new direct/gateway deployment over HTTP/WebSocket and JSON/Binary. The
consumer requires explicit Unknown values retaining `FUTURE`, mixed known/unknown
lists, round-trip resubmission and rejection of `UNDECLARED`. TS CJS/ESM and Dart
VM/JS/Wasm run this upgrade matrix. Kotlin and Swift run it using
`go run ./tests/sdk-interop -run -enum-upgrade [-gateway] -- <consumer>`:
the owner captures actual old introspection bytes and closes the old deployment,
then exposes that immutable snapshot at `LUXO_INTEROP_SCHEMA_ENDPOINT` solely for
generation. Requests use the separately compiled newer service at
`LUXO_INTEROP_ENDPOINT`; failures close both endpoints. The owner always sets
`LUXO_INTEROP_ENUM_UPGRADE` explicitly, preventing stale shell settings from
silently replacing ordinary tests. CI includes both upgrade topologies for all
four SDKs. This checks schema evolution, not compatibility with old wire versions
or older released SDK runtimes.

The same upgrade fixture also adds an API before existing declarations, inserts
a nullable `future` field at the start of `PageRecord`, and adds a defaulted
`delta: Int = 0` parameter to `echoInteger`. It first allocates IDs from the old
analyzed schema, then updates that same lock with the newer schema. Go compares
the two live introspection snapshots: existing API/parameter/model/type field IDs
must remain unchanged, and the new API/parameter must receive fresh IDs. An
explicit `delta` call verifies that the parameter actually affects execution.
All four unchanged old clients rerun pagination with Int64 extremes and partial
selection against the added model field. Existing scalar calls omit `delta`;
their values must remain unchanged over both modes and transports. The fixture
refuses missing replacement anchors instead of silently exercising identical
revisions. This is additive-schema compatibility with current SDK runtimes, not
a historical released-client or old-wire certification.

Each generated consumer also tests deliberately malformed local payloads:
truncation at every byte of a child and its enclosing message, missing end
markers, trailing bytes inside/outside children, and invalid nullable markers.
These negative probes supplement the real Go responses; they are not evidence
of malformed transport frames, multi-service relations or every scalar type.
Regenerate clients and update their compatible runtimes together: Dart and
Kotlin generated decoders now call the runtime's response completion checks.

From `packages/dart`, with the repository's Go, Dart and Node toolchains installed:

```sh
dart test test/go_interop_test.dart --reporter expanded
```

For TypeScript, build `packages/client` with `pnpm build`, then run from
`packages/vite-plugin`:

```sh
pnpm exec vitest run src/go-consumer.test.ts
```

For Kotlin, run from the core repository root with Go and JDK 26 installed:

```sh
go run ./tests/sdk-interop -run -- ./packages/kotlin/gradlew -p tests/sdk-interop/kotlin build --project-cache-dir .tmp/gradle-cache --no-daemon --warning-mode fail
go run ./tests/sdk-interop -run -gateway -- ./packages/kotlin/gradlew -p tests/sdk-interop/kotlin build --project-cache-dir .tmp/gradle-cache --no-daemon --warning-mode fail
```

For Swift on macOS, run from the core root. The SDK defaults to the sibling
`../luxo-swift`; override it with the path to the checkout being verified:

```sh
LUXO_SWIFT_PATH=/path/to/luxo-swift go run ./tests/sdk-interop -run -- bash tests/sdk-interop/swift/run.sh
LUXO_SWIFT_PATH=/path/to/luxo-swift go run ./tests/sdk-interop -run -gateway -- bash tests/sdk-interop/swift/run.sh
```

SwiftPM builds a small generator against that SDK, then generates and compiles a
separate consumer package under the runner's owned scratch directory. That consumer
does not depend on `LuxoCompiler` or the SDK's internal test interfaces. It checks
one-time schema registration in JSON and Binary, and waits asynchronously for streams.
The core CI checks out the Swift repository and runs this same command. Coordinated
changes require the compatible Swift revision to be available before the core PR
can pass; local sibling-checkout results do not certify the remote `main` revision.

The runner starts one generated service, or a gateway and separate RPC service,
passes the endpoint, corpus, topology and owned scratch directory to the consumer,
and closes its service/gateway processes on success or failure. On Linux/macOS,
build and consumer commands run in dedicated sessions with an anchor kept until
descendant cleanup finishes, preventing session-ID reuse during cleanup. The
consumer has a ten-minute deadline; success, failure and cancellation all clean
descendants even when tools create additional process groups. Inspection or
cleanup failure preserves the owned scratch directory and returns an error.
This requires process-inspection permissions and cooperative tools that do not
daemonize into separate sessions; it is not a sandbox for arbitrary detached
process trees. Other host systems explicitly reject this process runner. A real
Swift Foundation subprocess and separate-process-group regressions cover the
previously observed SwiftPM timeout cleanup failure.
Kotlin's stream test suspends while awaiting data, never blocking the socket
reader after a subscription acknowledgement. The corpus covers all four
HTTP/WebSocket × JSON/Binary channels. The Dart/TS suites are included in their
package tests; Kotlin and Swift have explicit CI steps. These are live loopback
correctness checks, not throughput or device/browser benchmarks.

`watchFiniteJSON` supplies 64 ordered rows through generated stream writers, then
returns normally or raises the generated `JSONRejected` error. TS, Dart, Kotlin and Swift generated
consumers verify exact large-integer/decimal/negative-zero tokens, one completion or
error notification after all rows, and a subsequent unary call on the same socket.
Dart checks terminal frames at UInt64.max on VM, JS and Wasm; Kotlin and Swift perform
the same boundary checks on the JVM and macOS respectively. Both the direct router
and gateway/RPC deployment are exercised. These terminal tests do not certify
device performance, typed stream-decoding error notifications or a decided
reconnect/replay policy.

The owner creates unique directories under the repository/package `.tmp`, binds
each owned process to `127.0.0.1:0`, closes its stdin to shut it down and removes its own
generated files in `finally`. It does not leave a service running. The generator
rejects nonempty output directories and destinations outside the core `.tmp`.

## 中文说明

该 fixture 从真实 `.luxo` 编译服务，不是 SDK 自己编解码后互相验证。实际使用
生成的处理器、Schema、行式/列式编码器和流编码器；普通 API 的逻辑仍由 Luxo
编译，只有流的数据源使用 `@native`。无需数据库、预制 HTTP 响应或新依赖。

Dart、TypeScript、Kotlin、Swift 测试从运行中的 Go 服务获取 Schema，生成并检查客户端。
Dart 覆盖 VM、JS、Wasm，TS 7 编译并运行 CJS/ESM 消费者，Kotlin 编译独立 JVM
消费者且不启用序列化编译插件。Swift 使用独立 SwiftPM 消费项目，仅依赖 SDK 公开
运行时产品，启用 Swift 6 严格并发与 warnings-as-errors。四者分别验证
HTTP/WebSocket 和 JSON/Binary，包括 Int64 极值、Duration、数组、nullable、
结构化输入、列式返回、显式字段选择及流。另以真实编译处理器验证结构化错误与内存模型分页；
四 SDK 另有下述真实网关/RPC 用例；多后端扩展合并与设备尚不由本套件证明。Dart 命令从 `packages/dart` 执行；TS 先在 `packages/client`
运行 `pnpm build`，再在 `packages/vite-plugin` 执行上面的 vitest 命令。Kotlin 命令
从核心仓库根目录执行，需要 Go 与 JDK 26；流数据使用非阻塞挂起等待，不阻塞确认帧
所在的收包线程。Swift 在 macOS 上从核心根目录执行上面的命令，默认 SDK 为相邻
`../luxo-swift`，也可指定待验收的 checkout。生成、构建产物位于 owner 管理的 `.tmp`；
验证两种模式初始化时只注册一次 Schema，流数据使用非阻塞等待。跨仓库联动需要先让
兼容的 Swift revision 可被 CI 获取，本地相邻 checkout 通过不等于远端 main 已通过。
Dart/TS 纳入包的默认测试，Kotlin/Swift 有独立 CI 步骤；这是本地真实服务
正确性验证，不是吞吐量或浏览器性能测试。

TS 7 的 CJS/ESM、Dart VM/JavaScript/Wasm、Kotlin JVM 和 Swift 消费者另启动独立网关与 TCP RPC 服务，复用成功响应和流的全部数值断言，
覆盖四条通道、原始 JSON 数字、Int64、Float 符号位、嵌套/列式、分页和 nullable 数组。
网关代理函数直接取自实际生成器，仅启动代码由 fixture 提供；网关不注册本地业务处理器。
测试验证两个进程、服务关闭后网关请求失败，以及成功/消费者失败/就绪失败后的资源关闭。
这次联测暴露并修复了生成参数表将命名枚举写成 Model、与 Schema Enum 不一致的问题；
不能再用 Schema 覆盖生成处理器的参数表来掩盖差异。
已有服务需要重新生成处理器；只升级运行时不会重写其静态参数表。

此前 `Missing error data` 已通过 unary RPC 原文透传修复；直连和网关均执行同一套完整错误断言，
保留 JSON 数字原文、完整 Int/Duration、错误标识与后续成功请求。Dart 三平台每种部署合计
231 项通过；Kotlin/Swift 的网关 HTTP/WebSocket × JSON/Binary 同样验证错误，不再跳过 data。
owner 验证消费者失败后关闭网关，并拒绝清理根目录、同级目录和符号链接。
Native 流终止错误、设备、完整协议及性能门禁仍须单独验收，不能由本轮通过推导冻结。
Go owner 根据实际部署
设置 `LUXO_INTEROP_TOPOLOGY` 并覆盖继承旧值；消费者拒绝缺失或未知的部署值，不做猜测。
CI 分开执行直连与网关，回归约束两种用例不能缺失、不能忽略失败；本地通过不代表远端 CI
或单独发布的 Swift revision 已通过，也不能作为完整协议冻结验收。

`rejectPrecision` 检查实际 422 错误中的完整 Int64、Unicode 数据，以及错误名、消息、
追踪 ID 和生产环境 cause 策略；TS/Dart 交错发送成功与错误请求，确认关联不串号。
`browsePrecision` 检查自定义默认页大小、显式模型字段选择、零/负数/过大页大小、页码归一化
与 Int64 最大页码。三个模型由 Luxo 在内存构造，不能据此证明数据库 LIMIT/OFFSET 或最大
total 的行为。`checkListEffects` 通过编译的局部函数，实际验证变量/函数返回/API 返回中的
列表元素只求值一次，并覆盖模型值、别名及返回模型指针的函数，不用手写 Go resolver 绕过。

`NullableArrays` 额外验证可空 Int/Duration/Boolean 数组的 null、空数组、有值和未选择
状态，覆盖单记录/列式返回，并用后续标量确认游标对齐；生成的 native 流也发送前三种
状态。`NestedPrecision` 另覆盖三层对象/对象列表、可空子对象/列表、Unicode、Int64
极值和部分选择，在行式、列式及 native 流中验证；输入保留独立的内联嵌套布局。

共享 `jsonNumberTokens` 的20个数字均按JSON根值和嵌套值验证，包括数组；覆盖256位
整数/小数、超过Int32及Int64范围的指数、指数前导零、小数尾随零和负零原文，不应
强制转为业务Float或Decimal。四个SDK都通过真实Go四通道回显。`rejectJSON`另通过编译的
Luxo 错误处理器返回每个原始 token，并检查错误标识/元数据及后续成功请求；
TS/Dart 还交错成功与错误。`watchJSON/watchJSONs` 在两种 WebSocket 模式的 native
单值/数组流保留相同根值与嵌套数字，列表含 JSON null。Dart `[JSON]` 生成 `List<Object?>`，
另有输入、行式、列式和 API 读取的 null 元素回归；不放宽业务 Int/Float 或外层
列表可空规则。新增 `ScalarArrays` 状态 3
检查负零、正负最小次正规数和最大有限值，覆盖行式/列式响应及 native 流；比较
包含符号位，不能让 `0 == -0` 掩盖问题。TS/Dart 还实际发送宿主语言直接构造的
通用 JSON 负零，而非仅转发解析过的原始 token。既有流状态没有改变。
外部 Kotlin 消费者另用本地负载验证非法数值不能进入
标量/列表/流回调或模型/分页结果，不能把这些拒绝用例当成真实 Go 畸形帧验收。

共享 `jsonNumberConversions` 为 40 组显式转换定义固定 Int64 十进制结果和 Float
的 16 位十六进制位模式；预期 null 表示转换必须拒绝，不表示原数字 token 无效。
覆盖半途舍入、正负零、次正规数/正规数边界、溢出及非零下溢，并检查转换不改原文。
Dart 在生成消费者内做 VM/JS/Wasm 本地转换检查，不能冒充每个向量都经过网络；
其他 SDK 的单元测试消费同一数据。Swift 既有 `LUXO_PROTOCOL_FIXTURE` 环境变量
让CI直接读取核心文件，并与内置副本逐字节比较，漏同步时直接失败。Swift共享Unicode
回归从同一入口消费全部23个用例，包括对象键与被覆盖值中的非法转义；仅通过本地
内置语料，不等同于与另一已发布核心版本完全一致。

`TestFixtureRawJSONNumericCorpus` 另将40个原始数字直接送入生成Go处理器，不先经SDK
宿主数字转换。9种Int/Duration/Float单值、列表及结构化响应形状，通过JSON
HTTP/WebSocket与两种部署验证1440个数值用例，另有768次非法输入后的成功请求。
WebSocket复用原连接并核验每次编号，同时检查所选字段、结果数量、整数及Float位模式。
这是实际网络与内部RPC转换验收，不冒充四SDK对全部token的网络、数据库、设备或性能验收。

`echoFixtureState` / `echoFixtureStates` 通过编译的 Luxo 逻辑返回命名枚举单值或
列表；对应 native 订阅共用 Go 生成编码器。四个 SDK 均验证两个已声明枚举值、空/
非空列表和 WebSocket 订阅，不传 `$select`。这证明命名枚举的 Schema 分流正确，
单靠这些已知成员样本不能证明未来新增成员的兼容行为。

Dart和Kotlin消费者现使用明确的Known/Unknown类型，另外通过单值/列表参数发送
未声明的`FUTURE`、空文本及错误大小写，要求两种传输、两种部署的当前Go服务端
均返回400。TS与Dart另构建两个真实Go服务版本：旧Schema仅含OPEN/CLOSED，新版
声明FUTURE并通过单值/列表流发送。客户端只从旧版生成，关闭旧服务后连接新版，
覆盖直连/网关、HTTP/WS和JSON/Binary；验证明确的Unknown、原值再次提交、混合
列表及服务端拒绝UNDECLARED。TS覆盖CJS/ESM，Dart覆盖VM/JS/Wasm；Kotlin/Swift
通过`-run -enum-upgrade`（网关另加`-gateway`）运行同类升级测试。测试所有者保存
旧服务真实Schema后关闭旧部署，以只读快照端点供生成器使用，业务请求仅发往实际
重新编译的新版；失败也清理两个端点。CI包含四SDK两种升级部署，但本地通过不代表
线上CI已执行通过。这不代表旧wire或历史已发布SDK运行库的兼容性。
同一升级夹具还在已有声明之前增加API、在PageRecord开头插入nullable字段，并给
echoInteger增加实际参与计算的`delta: Int = 0`。先由旧AST建立编号锁，再在同一锁
上更新新AST；Go比较真实新旧introspection中的API、参数、模型/type字段编号，
新增项不能挤占旧编号。四SDK旧客户端复用带Int64极值和部分选择的分页检查，
旧整数调用不提供delta；两种部署、两种模式及两种传输都必须保持原结果。
新增参数另有显式传值控制，源代码替换锚点失配时报错，不能悄悄测试同一版本。
其他形状的Unknown响应保留仍由独立生成消费者注入验证；契约已确定，但全部SDK
真实升级矩阵与性能验收未全部完成。

四个生成消费者另外用本地故意损坏的消息，验证子消息和父消息每个字节位置的截断、
缺少结束标记、内外尾随字节和非法 nullable 标记。负向探针只是补充，不能替代真实 Go
响应，也不代表坏传输帧、多服务关联或全部标量已验收。Dart/Kotlin 的生成解码器新增
运行时响应完成检查调用，消费者需同时更新兼容 SDK 并重新生成代码。

只启动一个随机端口回环服务，结束时关闭并清理本次专用 `.tmp` 目录。不会覆盖
已有生成目录，也不会保留后台服务。
