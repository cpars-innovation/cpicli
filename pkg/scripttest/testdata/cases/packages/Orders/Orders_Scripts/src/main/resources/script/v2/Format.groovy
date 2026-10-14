package script.v2

import com.sap.it.script.v2.api.Message

/** Pads an order number to the given width. */
String formatNo(String no, int width) {
    return no.trim().padLeft(width, '0')
}

/** Version 2 scripts have no getHeader: this fails on the runtime and in the stand-ins. */
Message wrongApi(Message message) {
    message.setProperty('x', message.getHeader('OrderNo', String))
    return message
}
