package com.sap.gateway.ip.core.customdev.processor;

/** Stand-in: the version namespace is neither SOAP 1.1 nor SOAP 1.2. */
public class UnknownSoapVersionException extends Exception {
    private static final long serialVersionUID = 1L;

    public UnknownSoapVersionException(String version) {
        super("unknown SOAP version " + version);
    }
}
