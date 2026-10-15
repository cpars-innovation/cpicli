package cpictl.standin

import groovy.json.JsonOutput
import groovy.json.JsonSlurper

/**
 * Runs script cases on the stand-ins: cpictl writes the plan (one entry per
 * case: script file, function, message, service answers) and reads the
 * results. Started by the Gradle task cpictlScriptCases:
 *
 *   gradle -q cpictlScriptCases -PcpictlPlan=plan.json -PcpictlResults=results.json
 *
 * Each case runs with a fresh message and CaseContext; nothing is sent
 * anywhere.
 */
class CaseRunner {
    static void main(String[] args) {
        def plan = new JsonSlurper().parse(new File(args[0]))
        def results = plan.cases.collect { runCase(it) }
        new File(args[1]).text = JsonOutput.toJson([cases: results])
    }

    static Map runCase(Map c) {
        def out = [id: c.id]
        def ctx = new CaseContext(c.pd ?: [:], c.valueMappings ?: [:], c.secure ?: [:], c.keystore ?: [:])
        CaseContext.set(ctx)
        def started = System.nanoTime()
        try {
            def binding = new Binding(messageLogFactory: new Services.LogFactory())
            def script = new GroovyShell(CaseRunner.classLoader, binding).parse(new File(c.script))
            def msg = c.version == 2 ? new MessageV2() : new MessageV1()
            if (c.body != null) {
                msg.setBody(c.body)
            }
            (c.headers ?: [:]).each { k, v -> msg.setHeader(k, v) }
            (c.properties ?: [:]).each { k, v -> msg.setProperty(k, v) }
            def result
            Services.Queue queue = null
            if (c.hasArgs) {
                // custom functions of message mappings also take a MappingContext and an
                // Output (the result queue of an all-values function): the runner supplies them
                def m = mappingFunction(script, c.function, (c.args ?: []).size())
                if (m == null) {
                    out.error = "the script has no function ${c.function} taking ${c.args?.size() ?: 0} argument(s) (plus MappingContext / Output)".toString()
                    return out
                }
                def given = (c.args ?: []).iterator()
                def values = m.parameterTypes.collect { type ->
                    if (com.sap.it.api.mapping.MappingContext.isAssignableFrom(type)) {
                        return new Services.Mapping(msg.getHeaders(), msg.getProperties())
                    }
                    if (com.sap.it.api.mapping.Output.isAssignableFrom(type)) {
                        queue = new Services.Queue()
                        return queue
                    }
                    org.codehaus.groovy.runtime.typehandling.DefaultTypeTransformation.castToType(given.next(), type)
                }
                try {
                    result = m.invoke(script, values as Object[])
                } catch (java.lang.reflect.InvocationTargetException e) {
                    throw e.cause
                }
            } else {
                try {
                    result = script.invokeMethod(c.function, msg)
                } catch (MissingMethodException e) {
                    if (e.method == c.function && e.type == script.getClass()) {
                        out.error = "the script has no function ${c.function} taking the message".toString()
                        return out
                    }
                    throw e
                }
            }
            if (queue != null) {
                out.returns = plain(queue.values())
                out.hasReturns = true
            } else if (c.hasArgs) {
                out.returns = plain(result)
                out.hasReturns = true
            } else if (result != null && !(result instanceof com.sap.gateway.ip.core.customdev.util.Message) && !(result instanceof com.sap.it.script.v2.api.Message)) {
                out.returns = plain(result)
                out.hasReturns = true
            }
            out.body = msg.getBody() == null ? null : msg.getBody(String)
            out.headers = msg.getHeaders().collectEntries { k, v -> [(k): v == null ? null : v.toString()] }
            out.properties = msg.getProperties().collectEntries { k, v -> [(k): v == null ? null : v.toString()] }
        } catch (Throwable t) {
            def cause = t instanceof org.codehaus.groovy.runtime.InvokerInvocationException && t.cause ? t.cause : t
            out.throws = [class: cause.getClass().name, message: cause.message ?: '']
            if (cause instanceof UnsupportedInStandInsException) {
                out.unsupported = true
            }
        } finally {
            out.durationMs = (System.nanoTime() - started).intdiv(1_000_000)
            out.log = [customHeaders: ctx.log.customHeaders ?: [:], properties: ctx.log.properties ?: [:]]
            CaseContext.clear()
        }
        return out
    }

    /**
     * The function to call with args: its parameters other than MappingContext and Output are
     * the arguments, in order.
     */
    static java.lang.reflect.Method mappingFunction(Script script, String name, int args) {
        script.getClass().methods.find { m ->
            m.name == name && m.parameterTypes.count { t ->
                !com.sap.it.api.mapping.MappingContext.isAssignableFrom(t) && !com.sap.it.api.mapping.Output.isAssignableFrom(t)
            } == args
        }
    }

    /** A JSON-able copy of a return value (maps, lists, scalars; other objects as text). */
    static Object plain(Object v) {
        switch (v) {
            case null: return null
            case Map: return v.collectEntries { k, x -> [(k.toString()): plain(x)] }
            case Collection: return v.collect { plain(it) }
            case Object[]: return v.collect { plain(it) }
            case Boolean: return v
            case BigDecimal: return v
            case Number: return v
            case CharSequence: return v.toString()
            default: return v.toString()
        }
    }
}
