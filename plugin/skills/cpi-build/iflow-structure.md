# Integration flow files

What `download_artifact` extracts and `upload_artifacts` expects:

```
<FlowId>/
├── META-INF/MANIFEST.MF              Bundle-SymbolicName = flow ID, Bundle-Name = display name,
│                                     Bundle-Version, SAP-BundleType: IntegrationFlow
├── metainfo.prop                     description
└── src/main/resources/
    ├── scenarioflows/integrationflow/<Name>.iflw    the model (BPMN 2.0 XML)
    ├── parameters.prop               externalised parameter values (Java properties, key=value;
    │                                 spaces in keys escaped as "\ ")
    ├── parameters.propdef            parameter definitions
    ├── script/                       Groovy (.groovy, .gsh) and JavaScript (.js) scripts
    ├── mapping/                      message mappings (.mmap), XSLT (.xsl, .xslt)
    ├── xsd/, wsdl/, json/, edmx/     schemas and service definitions
    └── ...
```

## The model (.iflw)

- `bpmn2:collaboration` holds the flow settings (properties such as `log`,
  `returnExceptionToSender`), the participants (sender and receiver systems) and the
  `bpmn2:messageFlow` elements, which are the **adapters** (`ComponentType` HTTPS, SOAP, SFTP,
  HTTP, ...; `direction` Sender/Receiver).
- `bpmn2:process` elements are the integration process and local processes. Steps are
  `bpmn2:callActivity` / `bpmn2:serviceTask` / events / gateways connected by
  `bpmn2:sequenceFlow` (`sourceRef`, `targetRef`, and `bpmn2:incoming`/`bpmn2:outgoing` in the steps).
- An exception subprocess is a `bpmn2:subProcess` whose type is `ErrorEventSubProcessTemplate`.
- Every element is configured by `<ifl:property><key>…</key><value>…</value></ifl:property>`
  entries in its `bpmn2:extensionElements`. `cmdVariantUri` names the component and version,
  e.g. `ctype::FlowstepVariant/cname::GroovyScript/version::1.1.2`
  (Enricher = Content Modifier, GroovyScript, MessageMapping, XSLTMapping, ...).
- **Groovy scripts: prefer the script step version 2** for new and changed scripts (Groovy 4).
  The step has `componentVersion` `2.0` and
  `cmdVariantUri` `ctype::FlowstepVariant/cname::GroovyScript/version::2.0.0`; its `script`
  property is `v2/<Name>.groovy` and the file lives in `src/main/resources/script/v2/`. The
  script starts with `package script.v2` and `import com.sap.it.script.v2.api.Message`
  (not `com.sap.gateway.ip.core.customdev.util.Message`, which is version 1). Moving a version 1
  script to version 2 is a rewrite for the new API, not a version change; lint reports version 1
  steps as `prefer-script-v2`. Keep a version 1 step only where its script must stay unchanged.
- `bpmndi:BPMNDiagram` holds the layout (shapes and edges with coordinates). Every element and
  sequence flow needs a shape/edge there, or the Web UI cannot display the flow. Never compute
  coordinates: run `layout_iflow` (or `cpictl iflow layout`) after editing; it draws missing
  shapes and lines and changes nothing outside the diagram.
- `{{Key}}` in a property value refers to an externalised parameter in parameters.prop.
- `get_message_log` with `include_steps` returns `modelStepId`: the `id` attribute of the failing element.

## Editing safely

- Change property values in place; keep `id`s unique and references (`sourceRef`, `targetRef`,
  `incoming`, `outgoing`, `processRef`, diagram `bpmnElement`) consistent.
- To add a step, copy a complete element of the same type from a flow of this tenant, including
  its diagram shape, give it a new unique id and rewire the sequence flows; then `layout_iflow`.
- Keep component versions (`componentVersion`, `cmdVariantUri`) as copied. For a new step or
  adapter take the `cmdVariantUri` and version from `list_components` (what the tenant offers;
  `componentVersion` is its major.minor, e.g. `1.22` for `.../version::1.22.0`); never a newer
  version than listed. The tenant's check (`validate_artifact`) reports unsupported combinations.
- Contents of table properties (headers and properties of a Content Modifier) are escaped XML
  inside the value (`&lt;row&gt;…`): keep the escaping.

## Copying a flow under a new ID

Use `copy_iflow`, not manual renaming: it sets `Bundle-SymbolicName`, `Bundle-Name` and
`Bundle-Version` in MANIFEST.MF (keeping `;singleton:=true` and the 72-byte line format), the
description in metainfo.prop, renames `<SourceID>.iflw` and `.project`, and changes every sender
address (in the model, or the parameter value in parameters.prop for a `{{parameter}}` address).
It refuses to keep a sender address unless `keep_addresses` is set: the same HTTP path or
ProcessDirect address fails the deployment, the same SFTP directory or JMS queue makes two flows
take each other's messages. Afterwards check the files listed in `remaining` (process names,
scripts that log the flow name).
