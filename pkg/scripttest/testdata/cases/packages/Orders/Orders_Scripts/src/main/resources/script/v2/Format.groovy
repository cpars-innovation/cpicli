package script.v2

import com.sap.it.script.v2.api.Message

/** Pads an order number to the given width. */
String formatNo(String no, int width) {
    return no.trim().padLeft(width, '0')
}

/** The version 2 API has no getHeader: this fails in the stand-ins (the runtime still accepts it). */
Message wrongApi(Message message) {
    message.setProperty('x', message.getHeader('OrderNo', String))
    return message
}
