package com.sap.it.api.mapping;

/** Stand-in: the result queue of an "all values of context" custom function. */
public interface Output {
    void addValue(Object value);

    void addSuppress();

    void addContextChange();
}
