package com.sap.it.script.v2.api.exceptions;

import java.util.List;
import org.xml.sax.SAXParseException;

/** Stand-in: a payload failed schema validation. */
public class SchemaValidationException extends ScenarioExecutionException {
    private static final long serialVersionUID = 1L;

    private final transient Object schema;
    private final transient List<SAXParseException> fatalErrors;
    private final transient List<SAXParseException> errors;
    private final transient List<SAXParseException> warnings;

    public SchemaValidationException(Object schema, List<SAXParseException> fatalErrors, List<SAXParseException> errors,
                                     List<SAXParseException> warnings) {
        super(message(schema, fatalErrors, errors, warnings));
        this.schema = schema;
        this.fatalErrors = fatalErrors;
        this.errors = errors;
        this.warnings = warnings;
    }

    public Object getSchema() {
        return schema;
    }

    public List<SAXParseException> getErrors() {
        return errors;
    }

    public List<SAXParseException> getFatalErrors() {
        return fatalErrors;
    }

    public List<SAXParseException> getWarnings() {
        return warnings;
    }

    protected static String message(Object schema, List<SAXParseException> fatalErrors, List<SAXParseException> errors,
                                    List<SAXParseException> warnings) {
        StringBuilder b = new StringBuilder("Validation failed for: ").append(schema);
        append(b, "fatal errors", fatalErrors);
        append(b, "errors", errors);
        append(b, "warnings", warnings);
        return b.toString();
    }

    private static void append(StringBuilder b, String label, List<SAXParseException> list) {
        if (list != null && !list.isEmpty()) {
            b.append("\n").append(label).append(":");
            for (SAXParseException e : list) {
                b.append("\n  ").append(e.getMessage());
            }
        }
    }
}
