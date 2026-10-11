// Groovy script step version 2.x (Groovy 4)
package script.v2

import com.sap.it.script.v2.api.Message

def Message processData(Message message) {
    String body = message.getBody(String) ?: ''
    message.setBody(body.replace('<Order>', '<Invoice>').replace('</Order>', '</Invoice>'))
    return message
}
