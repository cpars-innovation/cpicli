package com.sap.it.api.pd;

/** Stand-in: a binary Partner Directory parameter. */
public interface BinaryData {
    String getContentType();

    byte[] getData();
}
