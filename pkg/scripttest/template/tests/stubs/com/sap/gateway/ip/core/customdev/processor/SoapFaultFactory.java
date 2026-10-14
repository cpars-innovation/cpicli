package com.sap.gateway.ip.core.customdev.processor;

import java.util.List;
import java.util.Map;
import java.util.Set;
import javax.xml.namespace.QName;
import javax.xml.stream.XMLStreamException;
import org.w3c.dom.Element;
import cpictl.standin.SoapFaultException;

/**
 * Stand-in: throws SOAP faults. On the runtime the fault goes back to the SOAP sender; in the stand-ins
 * the case ends with {@link SoapFaultException} (expect it with {@code throws}).
 */
public class SoapFaultFactory {
    public static final String SOAP11_NAMESPACE = "http://schemas.xmlsoap.org/soap/envelope/";
    public static final String SOAP12_NAMESPACE = "http://www.w3.org/2003/05/soap-envelope";

    private static final Set<String> SOAP11_CODES = Set.of("VersionMismatch", "MustUnderstand", "Client", "Server");
    private static final Set<String> SOAP12_CODES =
            Set.of("VersionMismatch", "MustUnderstand", "DataEncodingUnknown", "Sender", "Receiver");

    public SoapFaultFactory() {}

    public static void throwSoapFault(SoapFaultParam param) throws UnknownSoapVersionException, UnknownFaultCodeException {
        String ns = param.getVersionNamespace() == null ? SOAP11_NAMESPACE : param.getVersionNamespace();
        Set<String> codes;
        if (SOAP11_NAMESPACE.equals(ns)) {
            codes = SOAP11_CODES;
        } else if (SOAP12_NAMESPACE.equals(ns)) {
            codes = SOAP12_CODES;
        } else {
            throw new UnknownSoapVersionException(ns);
        }
        QName code = param.getFaultcode();
        if (code == null || !codes.contains(code.getLocalPart())) {
            throw new UnknownFaultCodeException(code);
        }
        throw new SoapFaultException(code, param.getFaultstring(), param.getFaultactor(), param.getDetail(),
                param.getSubcodes() == null ? List.of() : param.getSubcodes());
    }

    public static void throwSoapFault(String faultstring, Element detail, Map<String, String> namespaces) {
        throw new SoapFaultException(new QName(SOAP11_NAMESPACE, "Server"), faultstring, null, detail, List.of());
    }

    public static void throwXiSoapFault(XiSoapFaultParam param) throws XMLStreamException {
        QName code = param.getFaultcode() == null ? new QName(SOAP11_NAMESPACE, "Server") : param.getFaultcode();
        throw new SoapFaultException(code, param.getFaultstring(), param.getFaultactor(), null, List.of());
    }
}
