package com.sap.it.api.mapping;

/** Stand-in: headers and properties for custom functions of a message mapping. */
public interface MappingContext {
    String getHeader(String name);

    String getProperty(String name);

    void setHeader(String name, Object value);

    void setProperty(String name, Object value);
}
