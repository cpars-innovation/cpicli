# SAP script API stand-ins

Java classes with the package and class names of SAP Cloud Integration's script API, so Groovy
scripts of script collections and flows compile and run in a plain Gradle build (`cpictl test
scripts`) without a SAP jar. `cpictl test scripts init` writes them to `tests/stubs/` together with
`build.gradle`. They are test code: never upload them (the folder is outside the artifact folders).

Requires JDK 17 or newer (records, pattern matching in `instanceof`). The libraries they import
(Camel attachments, `javax.activation`, Bouncy Castle) are in the generated `build.gradle`.

The stand-ins follow SAP's split: the SAP packages hold **interfaces and plain classes**, as SAP
publishes them. The implementations that tests create live in `cpictl.standin`.

## What is covered

| Area | Classes (package) | Test-facing implementation |
|------|-------------------|----------------------------|
| Message v1 | `com.sap.gateway.ip.core.customdev.util`: `Message`, `AttachmentWrapper`, `SoapHeader` | `cpictl.standin.MessageV1` |
| Message v2 | `com.sap.it.script.v2.api`: `Message`, `Exchange`, `Attachment`, `CamelContext`, `Expression`, `Language`, `attachment.*`, `extensions.*`, `language.*`, `exceptions.*` | `cpictl.standin.MessageV2` (`exchange()` gives its `Exchange`) |
| SOAP faults | `com.sap.gateway.ip.core.customdev.processor`: `SoapFaultFactory`, `SoapFaultParam`, `XiSoapFaultParam`, fault exceptions | `cpictl.standin.SoapFaultException` is thrown |
| Message log | `com.sap.it.api.msglog`: `MessageLog`, `MessageLogFactory` | `cpictl.standin.Services.LogFactory` (the binding `messageLogFactory`) |
| Services | `com.sap.it.api`: `ITApiFactory`, `ITApi`; `com.sap.it.api.impl.ITApiFactoryRegistry`, `com.sap.it.spi.ITApiHandler` | answer from `CaseContext` |
| Secure store | `com.sap.it.api.securestore.*` | user credentials and tokens from `CaseContext` |
| Keystore | `com.sap.it.api.keystore.*` | PEM material or a generated test key from `CaseContext` |
| Partner Directory | `com.sap.it.api.pd.*` | string and binary parameters from `CaseContext` |
| Value mapping | `com.sap.it.api.mapping.ValueMappingApi` | mappings from `CaseContext` |
| Number ranges | `com.sap.it.api.nrc.*` | each range counts from 1 per context |
| Data store | `com.sap.it.api.asdk.datastore.*`, `com.sap.it.api.asdk.runtime.Factory` | in memory, empty per context |
| Mapping functions | `com.sap.it.api.mapping.MappingContext`, `Output` | `Services.Mapping`, `Services.Queue` |

Message bodies convert like Camel's type converters do for the types scripts ask for (`String`,
`byte[]`, `InputStream`, `Reader`, DOM `Document` / `Element` / `Node`, XML `Source`, numbers,
booleans). Headers are case-insensitive, properties are case-sensitive, as on the runtime. Anything
a stand-in does not implement (a service it lacks, a conversion outside that list, a Simple
expression it does not know) throws `cpictl.standin.UnsupportedInStandInsException`: that is not a
failure of your script, test that part on the tenant.

## Setting values in a test

```groovy
import cpictl.standin.CaseContext
import cpictl.standin.MessageV1
import cpictl.standin.Services

class HeadersTest {
    @AfterEach
    void clear() { CaseContext.clear() }

    @Test
    void setsOrderHeader() {
        def ctx = CaseContext.builder()
            .pd('PID1', 'Target', 'ERP-DEV')                       // Partner Directory string parameter
            .valueMapping('A', 'Order', 'x', 'B', 'Order', 'y')    // source agency/id/value -> target agency/id
            .secure('erp-user', 'alice', 'test-password')          // secure store user credential
            .keystore('signing', 'generate')                       // PEM text, or a generated test key
            .build().install()                                     // active for this thread

        def msg = new MessageV1(headers: [OrderNo: '4711'], properties: [PID: 'PID1'])
        def script = new Headers()
        script.binding = new Binding(messageLogFactory: new Services.LogFactory())
        script.setOrderHeader(msg)

        assert ctx.log.customHeaders.OrderNo == ['4711']           // what the script wrote to the message log
    }
}
```

- `CaseContext` is thread-local: `install()` sets it for the current thread (and threads the test
  starts), `CaseContext.clear()` removes it. Without one, the services answer from an empty context
  (a missing PD parameter is `null`, a missing credential throws `CredentialNotFoundException`).
- Values are test values only. Never put a real credential or key into a test: tests are committed.
- `ctx.log` holds what the script wrote to the message log: `properties`, `customHeaders` (name to
  list of values) and `attachments` (name to `{mediaType, text}`).
- PD binary parameters: pass base64 text through the `CaseContext` constructor's `pd` map; a value
  that is not base64 is taken as text. Value mapping keys are `srcAgency|srcId|value|tgtAgency|tgtId`.
- The `new Message()` of the three minimal stubs older versions of `init` wrote is now
  `new MessageV1()` (v1 scripts) or `new MessageV2()` (v2 scripts); `Message` is an interface, as at SAP.
  The message log is reached through `messageLogFactory` and `ctx.log`, not through the message.

## What is verified, what is not

The signatures were written from SAP's published API documentation (the javadoc of the SAP script
API on Maven Central and help.sap.com); no SAP jar was copied. Conformance tests in the cpicli
repository (`pkg/scripttest/testdata/conformance`, run with the Gradle build) check the documented
behaviour of the classes.

**Not verified: the signatures of these are not in SAP's javadoc.** They are the best reading of how
scripts use them and may differ from the runtime:

- the data store classes (`DataStoreService`, `DataBean`, `DataConfig`, `MetaData`,
  `DataStoreException`, `Factory`), including how `expires` and `retentionThreshold` are interpreted;
- `ValueMappingApi.getMappedValues` (the stand-in returns a list with the single mapped value, or an
  empty list);
- `MappingContext` and `Output` (the methods and the markers `addSuppress` / `addContextChange`).

Nothing here has been compared with a running tenant either: nobody has run a probe flow that prints
the runtime's signatures and results. If you have a tenant that deploys flows, such a probe settles
the open points; please report differences.

## Extending

- A missing method of a SAP class: add it to the interface in the SAP package and to the
  implementation in `cpictl.standin` (`MessageCore`, `MessageV1`, `MessageV2`, `Services`). Keep the
  SAP signature exactly; if you are not sure of it, say so in a comment and list it above.
- A missing service: add the interface (package as at SAP), an implementation in `Services`, and a
  branch in `Services.get`. Or bind your own without touching the library:
  `new ITApiFactoryRegistry().bindApiFactory(handler, [api: MyService])`.
- `cpictl test scripts init` keeps existing files (`--force` overwrites them). Keep your own
  additions in a separate package or in a commit that is easy to re-apply after `init --force`.
- Script file names must stay unique across the collections the build compiles, and no script may
  be named like a class below `tests/stubs`.
