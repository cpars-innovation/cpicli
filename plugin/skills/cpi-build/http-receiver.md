# HTTP receivers

Lessons from flows that call REST and OData APIs through an API gateway. Check them before adding or
changing an HTTP receiver.

## Read the API definition first

- Take request bodies from the API's OpenAPI definition, not from habit: some OData services want
  PATCH bodies as `{"d": {...}}` and POST bodies flat, and a gateway that validates the schema
  answers anything else with a 400. Check field names and `maxLength` in the schema the operation
  references.

## Address, query and routing headers

- Build the address in the receiver: `{{API_HOST}}${property.path}`. A parameter followed by a
  runtime expression counts as externalised.
- The adapter re-encodes the address (`%20` becomes `+`). To send a query exactly as given, keep it
  out of the address and put the raw query into the `CamelHttpQuery` header (`$filter`, plain `'`,
  spaces as `%20`).
- Clear `CamelHttpUri` (it replaces the whole address), `CamelHttpPath` and `CamelHttpQuery`
  (both are appended) before every receiver call. Messages that came in over HTTP or an event broker
  carry them and silently change the target URL.
- Keep the allowed request headers to what the call needs (tokens, cookies, content type, accept,
  if-match, gateway key). Tracing headers must not leave the flow; `Authorization` comes from the
  credential, not from a message header.

## Method and body

- The method can be dynamic: `httpMethod` `DYNAMIC` with `methodSourceExpression`
  `${header.CamelHttpMethod}`. A fixed method plus a `CamelHttpMethod` header also works but shows
  the wrong method in the editor.
- "Send body" is a fixed switch, not an expression, and gateways reject GET, HEAD and DELETE
  requests with a body. With a dynamic method, route to two receivers: body off for GET/HEAD/DELETE,
  on for POST/PUT/PATCH.
- The exchange keeps the previous step's body and `Content-Type`: empty the body and remove
  `Content-Type` before a GET (for example after a mapping).
- The credential name may be dynamic (`${property.credential}`) when the caller supplies it.

## CSRF

- Fetch with `X-CSRF-Token: Fetch` on a GET (or HEAD where allowed), keep the token and the cookies,
  send both on the write. When you cache them (for example in a global variable, encrypted, with an
  expiry), key the cache by client, credential and gateway key: a token belongs to one backend
  session. Retry once after a 403 with `X-CSRF-Token: Required`.

## Diagnosing a 4xx

- The message log shows the target URL and the adapter steps, not the request headers. Ask early
  for the gateway's log entry (response code, backend response code, which side answered).
- Compare the failing request with a working one (a REST client, another flow) header by header,
  body included, before changing anything.
- Put the credential and key alias names and the first part of the response body into the error
  text: alias names are not secrets.
