import com.sap.it.api.mapping.MappingContext
import com.sap.it.api.mapping.Output

// Custom functions of the flow's message mapping.

/** Turns 15.10.2026 into 2026-10-15, with the separator from the header DateSep (default -). */
def String formatDate(String d, MappingContext context) {
    def sep = context.getHeader('DateSep') ?: '-'
    def (day, month, year) = d.split('\\.')
    return [year, month, day].join(sep)
}

/** All values of a context: upper-cases each, suppresses empty ones. */
def void upperAll(String[] values, Output output, MappingContext context) {
    values.each { v -> v ? output.addValue(v.toUpperCase()) : output.addSuppress() }
}
