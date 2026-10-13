# Script collections: doc comments and tests

A script collection is a library: flows call its functions, and a change reaches every flow that
uses it. Each function therefore says what it does and how to call it, and tests cover it. lint
checks this:

| Rule | Checks |
|------|--------|
| `collection-script-doc` | every public function of a collection script has a doc comment |
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
tests/stubs/...                     # Message stubs (see below), shared by all tests
build.gradle, settings.gradle       # test build at the project root
```

Never put tests or stubs into an artifact folder: upload sends the whole folder to the tenant, and
snapshot replaces the folder with the tenant's content.

Each test calls every public function with a normal message and with the edge cases the comment
names (missing header, empty body, wrong type). A script class is instantiated with `new`; set
bindings such as `messageLogFactory` through `binding`:

```groovy
import com.sap.gateway.ip.core.customdev.util.Message
import com.sap.gateway.ip.core.customdev.util.MessageLogFactory
import org.junit.jupiter.api.Test
import static org.junit.jupiter.api.Assertions.*

class HeadersTest {
    Headers script() {
        def s = new Headers()
        s.binding = new Binding(messageLogFactory: new MessageLogFactory())
        s
    }

    @Test
    void setsOrderHeader() {
        def msg = new Message(headers: [OrderNo: '4711'])
        script().setOrderHeader(msg)
        assertEquals('4711', msg.messageLog.customHeaders['OrderNo'])
        assertEquals('4711', msg.getHeader('OrderNo', String))
    }

    @Test
    void missingOrderNoFails() {
        assertThrows(IllegalStateException) { script().setOrderHeader(new Message()) }
    }
}
```

## Stubs

The SAP script API is not on Maven Central. The tests use small stub classes with the same package
and class names, holding only the methods the scripts call (extend them when a script needs more).
Write them in **Java**: a Groovy class with `getProperty(String)` would replace Groovy's own
property access. They are test code: never upload them.

`tests/stubs/com/sap/gateway/ip/core/customdev/util/Message.java` (version 1 scripts; for version 2
scripts the same class in `tests/stubs/com/sap/it/script/v2/api/` with
`package com.sap.it.script.v2.api;`):

```java
package com.sap.gateway.ip.core.customdev.util;

import java.util.HashMap;
import java.util.Map;

public class Message {
    private Object body;
    private Map<String, Object> headers = new HashMap<>();
    private Map<String, Object> properties = new HashMap<>();
    private final MessageLog messageLog = new MessageLog();

    public Object getBody() { return body; }
    @SuppressWarnings("unchecked")
    public <T> T getBody(Class<T> type) {
        if (body == null) return null;
        if (type == String.class) return (T) body.toString();
        return type.cast(body);
    }
    public void setBody(Object body) { this.body = body; }
    public Map<String, Object> getHeaders() { return headers; }
    public void setHeaders(Map<String, Object> headers) { this.headers = new HashMap<>(headers); }
    @SuppressWarnings("unchecked")
    public <T> T getHeader(String name, Class<T> type) {
        Object v = headers.get(name);
        if (v == null) return null;
        if (type == String.class) return (T) v.toString();
        return type.cast(v);
    }
    public void setHeader(String name, Object value) { headers.put(name, value); }
    public Map<String, Object> getProperties() { return properties; }
    public void setProperties(Map<String, Object> properties) { this.properties = new HashMap<>(properties); }
    public Object getProperty(String name) { return properties.get(name); }
    public void setProperty(String name, Object value) { properties.put(name, value); }
    public MessageLog getMessageLog() { return messageLog; }
}
```

`tests/stubs/com/sap/gateway/ip/core/customdev/util/MessageLog.java`:

```java
package com.sap.gateway.ip.core.customdev.util;

import java.util.HashMap;
import java.util.Map;

public class MessageLog {
    private final Map<String, String> customHeaders = new HashMap<>();
    private final Map<String, String> stringProperties = new HashMap<>();
    private final Map<String, String> attachments = new HashMap<>();

    public void addCustomHeaderProperty(String name, String value) { customHeaders.put(name, value); }
    public void setStringProperty(String name, String value) { stringProperties.put(name, value); }
    public void addAttachmentAsString(String name, String text, String type) { attachments.put(name, text); }
    public Map<String, String> getCustomHeaders() { return customHeaders; }
    public Map<String, String> getStringProperties() { return stringProperties; }
    public Map<String, String> getAttachments() { return attachments; }
}
```

`tests/stubs/com/sap/gateway/ip/core/customdev/util/MessageLogFactory.java` (scripts reach it
through the `messageLogFactory` binding; the test sets it):

```java
package com.sap.gateway.ip.core.customdev.util;

public class MessageLogFactory {
    public MessageLog getMessageLog(Message message) { return message.getMessageLog(); }
}
```

## Build

`build.gradle` at the project root. It compiles the collection scripts in place, so they are tested
exactly as they are uploaded, and writes the JaCoCo report that lint reads. Check the versions
against what your tenant runs (version 1 scripts: Groovy 2.4; version 2: Groovy 4):

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

Run `gradle test` (or `./gradlew test`), then `cpictl lint` (MCP `lint`). In CI: test first, then
lint, so the coverage rule sees the current report. Script file names must be unique across the
collections the build compiles (the class name is the file name).

## Workflow for an agent

1. Before writing a new function, look for an existing one: `graph_search` (type `script`) with
   words of the purpose; reuse it when its comment fits.
2. Write or change the function together with its doc comment.
3. Write or extend the test in `tests/scripts/<package>/<collection>/`; create the stubs and
   `build.gradle` from this page if the repository has none (ask before adding a build to a
   repository that has its own).
4. Run the tests (`gradle test`) when a JDK and Gradle are available; otherwise say so in the
   summary, so a person or CI runs them.
5. `lint` on the collection: no `collection-script-doc`, `collection-script-tests` or
   `collection-script-coverage` findings.
