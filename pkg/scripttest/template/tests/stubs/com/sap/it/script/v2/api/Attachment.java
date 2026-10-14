package com.sap.it.script.v2.api;

import java.util.Collection;
import java.util.List;
import javax.activation.DataHandler;

/** Stand-in: an attachment of a version 2 message. */
public interface Attachment {
    DataHandler getDataHandler();

    String getHeader(String name);

    List<String> getHeaderAsList(String name);

    Collection<String> getHeaderNames();

    void setHeader(String name, String value);

    void addHeader(String name, String value);

    void removeHeader(String name);

    void clearHeaders();
}
