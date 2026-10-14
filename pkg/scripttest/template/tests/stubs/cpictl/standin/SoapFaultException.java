package cpictl.standin;

import java.util.List;
import javax.xml.namespace.QName;
import org.w3c.dom.Element;

/** The SOAP fault a script threw through SoapFaultFactory (the runtime throws CXF's SoapFault). */
public class SoapFaultException extends RuntimeException {
    private static final long serialVersionUID = 1L;

    private final transient QName faultCode;
    private final String faultActor;
    private final transient Element detail;
    private final transient List<QName> subcodes;

    public SoapFaultException(QName faultCode, String faultString, String faultActor, Element detail, List<QName> subcodes) {
        super(faultString);
        this.faultCode = faultCode;
        this.faultActor = faultActor;
        this.detail = detail;
        this.subcodes = subcodes;
    }

    public QName getFaultCode() {
        return faultCode;
    }

    public String getFaultActor() {
        return faultActor;
    }

    public Element getDetail() {
        return detail;
    }

    public List<QName> getSubcodes() {
        return subcodes;
    }
}
