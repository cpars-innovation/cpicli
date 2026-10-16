# Script collections: doc comments and tests

A script collection is a library: flows call its functions, and a change reaches every flow that
uses it. Each function therefore says what it does and how to call it, and tests cover it. lint
checks this:

| Rule | Checks |
|------|--------|
| `collection-script-doc` | every public function of a collection script has a doc comment |
| `collection-script-doc-quality` | the comments say something and name every header and property the code reads or writes ([script-docs.md](script-docs.md)) |
| `collection-script-keywords` (info) | the script has `@keywords` |
| `collection-script-tests` | every public function is called by a test in `tests/scripts/<package>/<collection>/` |
| `collection-script-coverage` | line coverage per script in the JaCoCo report is at least `scriptTests.minCoverage` (default 80) |
| `script-doc` (info) | a flow's local script says what it does |

Settings in `.cpi/lint.yaml` (paths relative to the project root, the folder that holds `.cpi`):

```yaml
scriptTests:
  dir: tests/scripts                                              # default
  coverageReport: build/reports/jacoco/test/jacocoTestReport.xml  # default
  minCoverage: 80                                                 # default, percent of lines
```

## Doc comments

Above every public function, a `/** */` comment. The first sentence is what discovery shows and what
`graph_search` finds (`graph_search` with type `script` lists collection scripts with their
functions), so it names the purpose in words people search for. Then what the function reads and
writes, the errors it throws, and how a step calls it:

```groovy
/**
 * Sets the order number as custom header property OrderNo for monitoring.
 *
 * Reads:  header OrderNo (required), property SAP_ApplicationID (optional).
 * Writes: custom header property OrderNo; header SAP_ApplicationID when it was empty.
 * Throws: IllegalStateException when OrderNo is missing.
 * Call:   Groovy Script step, script collection Orders_Scripts, file Headers.groovy,
 *         function setOrderHeader.
 */
def Message setOrderHeader(Message message) {
```

- The file starts with a comment that says what the script groups ("Header helpers for order
  flows").
- Helpers that flows must not call are `private`; they need no doc comment and no direct test.
- Flow scripts (not in a collection) get at least one sentence at the top or above
  `processData` (`script-doc`).
- When you change a function's behaviour, change its comment in the same edit.

## Tests

Tests live outside the artifact folders, in the same layout as the content tree:

```
packages/Orders/Orders_Scripts/src/main/resources/script/Headers.groovy
tests/scripts/Orders/Orders_Scripts/HeadersTest.groovy
tests/stubs/...                     # SAP script API stand-ins (see below), shared by all tests
build.gradle, settings.gradle       # test build at the project root
```

Never put tests or stubs into an artifact folder: upload sends the whole folder to the tenant, and
snapshot replaces the folder with the tenant's content.

Each test calls every public function with a normal message and with the edge cases the comment
names (missing header, empty body, wrong type). A script class is instantiated with `new`; set
bindings such as `messageLogFactory` through `binding`:

```groovy
import cpictl.standin.CaseContext
import cpictl.standin.MessageV1
import cpictl.standin.Services
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.Test
import static org.junit.jupiter.api.Assertions.*

class HeadersTest {
    @AfterEach
    void clear() { CaseContext.clear() }

    Headers script() {
        def s = new Headers()
        s.binding = new Binding(messageLogFactory: new Services.LogFactory())
        s
    }

    @Test
    void setsOrderHeader() {
        def ctx = CaseContext.builder().build().install()
        def msg = new MessageV1(headers: [OrderNo: '4711'])
        script().setOrderHeader(msg)
        assertEquals(['4711'], ctx.log.customHeaders['OrderNo'])
        assertEquals('4711', msg.getHeader('OrderNo', String))
    }

    @Test
    void missingOrderNoFails() {
        assertThrows(IllegalStateException) { script().setOrderHeader(new MessageV1()) }
    }
}
```

## Stubs

`cpictl test scripts init` writes the stand-ins below and `build.gradle` into the project (existing
files are kept; `--force` replaces them).

The SAP script API is not on Maven Central. The tests use stand-in classes with the same package and
class names, written in **Java** (a Groovy class with `getProperty(String)` would replace Groovy's own
property access). They are test code: never upload them. `tests/stubs/README.md`, written with them,
is the reference: what each stand-in covers, how a test sets the values the services answer with, which
signatures are verified against SAP's javadoc and which are not, and how to extend them.

- `Message` v1 (`com.sap.gateway.ip.core.customdev.util`) and v2 (`com.sap.it.script.v2.api`) are
  interfaces, as at SAP. A test creates `new MessageV1()` or `new MessageV2()` (`cpictl.standin`);
  Groovy's map constructor sets body, headers and properties: `new MessageV1(headers: [OrderNo: '4711'])`.
- Version 2 scripts (`script/v2/`, `package script.v2`) have no `getHeader(...)` (version 1 only):
  they read `message.getHeaders().get("name")`. The v2 stand-in lacks it too, so a test fails when a
  version 2 script calls it.
- Scripts reach the message log as the binding `messageLogFactory`: `new Services.LogFactory()`. What
  a script logged is `CaseContext.current().log`.
- `ITApiFactory.getService(SecureStoreService | KeystoreService | PartnerDirectoryService |
  ValueMappingApi | NumberRangeConfigurationService, ctx)` and the data store answer from the values
  the test sets: `CaseContext.builder().pd(...).valueMapping(...).secure(...).keystore(...).build().install()`
  (thread-local; `CaseContext.clear()` after the test). Test values only, never real secrets.
- Something a stand-in does not implement throws `UnsupportedInStandInsException`: test that part on
  the tenant, or extend the stand-in (README).

## Build

`build.gradle` at the project root. It compiles the collection scripts in place, so they are tested
exactly as they are uploaded, and writes the JaCoCo report that lint reads. Check the versions
against what your tenant runs (version 1 scripts: Groovy 2.4; version 2: Groovy 4). JDK 17 or newer
compiles the stand-ins:

```groovy
plugins {
    id 'groovy'
    id 'jacoco'
}

repositories { mavenCentral() }

dependencies {
    implementation 'org.apache.groovy:groovy:4.0.21'
    implementation 'org.apache.groovy:groovy-json:4.0.21'  // JsonSlurper, JsonOutput (the runtime has them)
    implementation 'org.apache.groovy:groovy-xml:4.0.21'   // XmlSlurper, XmlUtil, MarkupBuilder
    // libraries of the SAP script API stand-ins (tests/stubs), the versions the CPI runtime ships
    implementation 'org.apache.camel:camel-attachments:3.14.10'   // Attachment, DefaultAttachment (also brings camel-api, camel-support)
    implementation 'com.sun.activation:javax.activation:1.2.0'     // DataHandler, DataSource
    implementation 'org.slf4j:slf4j-api:1.7.36'
    implementation 'org.bouncycastle:bcprov-jdk18on:1.78.1'        // keystore stand-in: test key and certificate generation
    implementation 'org.bouncycastle:bcpkix-jdk18on:1.78.1'
    implementation 'org.bouncycastle:bcutil-jdk18on:1.78.1'
    testImplementation 'org.junit.jupiter:junit-jupiter:5.10.2'
    testRuntimeOnly 'org.junit.platform:junit-platform-launcher'
}

// the script folders of all script collections below packages/
def collectionScriptDirs = fileTree('packages') { include '**/META-INF/MANIFEST.MF' }.files
    .findAll { it.text.contains('SAP-BundleType: ScriptCollection') }
    .collect { new File(it.parentFile.parentFile, 'src/main/resources/script') }
    .findAll { it.directory }

sourceSets {
    main { groovy { srcDirs = collectionScriptDirs + ['tests/stubs'] } }
    test { groovy { srcDirs = ['tests/scripts'] } }
}

test {
    useJUnitPlatform()
    finalizedBy jacocoTestReport
}

jacocoTestReport {
    reports { xml.required = true; html.required = true }
}
```

Run `cpictl test scripts` (runs `./gradlew test` or `gradle test` and reports every test and every
collection function: coverage, the tests that call it, passed/failed/untested; `--output json`),
then `cpictl lint` (MCP `lint`). In CI: test first, then
lint, so the coverage rule sees the current report. Script file names must be unique across the
collections the build compiles (the class name is the file name).

## Workflow for an agent

1. Before writing a new function, look for an existing one: `graph_search` (type `script`) with
   words of the purpose; reuse it when its comment fits.
2. Write or change the function together with its doc comment.
3. Write or extend the test in `tests/scripts/<package>/<collection>/`; run `cpictl test scripts init` for the
   stubs and `build.gradle` if the repository has none (ask before adding a build to a
   repository that has its own).
4. Run the tests (`cpictl test scripts`) when a JDK and Gradle are available; otherwise say so in the
   summary, so a person or CI runs them.
5. `lint` on the collection: no `collection-script-doc`, `collection-script-tests` or
   `collection-script-coverage` findings.
