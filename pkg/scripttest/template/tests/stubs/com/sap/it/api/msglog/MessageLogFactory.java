package com.sap.it.api.msglog;

/** Stand-in: gives the message log of a message (scripts get it as the binding messageLogFactory). */
public interface MessageLogFactory {
    MessageLog getMessageLog(Object context);
}
