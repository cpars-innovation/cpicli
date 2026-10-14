package com.sap.gateway.ip.core.customdev.processor;

import javax.xml.namespace.QName;

/** Stand-in: the fault code is not one of the SOAP version's codes. */
public class UnknownFaultCodeException extends Exception {
    private static final long serialVersionUID = 1L;

    public UnknownFaultCodeException(QName faultCode) {
        super("unknown SOAP fault code " + faultCode);
    }
}
