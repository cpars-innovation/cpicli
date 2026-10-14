import com.sap.gateway.ip.core.customdev.util.Message
import com.sap.it.api.ITApiFactory
import com.sap.it.api.pd.PartnerDirectoryService

/** Sets the order number as custom header and the target system from the Partner Directory. */
def Message setOrderHeader(Message message) {
    def orderNo = message.getHeader('OrderNo', String)
    if (!orderNo) throw new IllegalStateException('OrderNo is missing')
    def pd = ITApiFactory.getService(PartnerDirectoryService, null)
    message.setProperty('Target', pd.getParameter('Target', message.getProperty('PID'), String))
    messageLogFactory.getMessageLog(message).addCustomHeaderProperty('OrderNo', orderNo)
    message
}
