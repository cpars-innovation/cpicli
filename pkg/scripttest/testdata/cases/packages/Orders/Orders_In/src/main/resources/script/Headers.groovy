import com.sap.gateway.ip.core.customdev.util.Message
import com.sap.it.api.ITApiFactory
import com.sap.it.api.pd.PartnerDirectoryService

// Trims the order number header, logs it and reads the target system from the Partner Directory.
def Message processData(Message message) {
    def no = message.getHeader('OrderNo', String)?.trim()
    if (!no) {
        throw new IllegalStateException('OrderNo missing')
    }
    message.setHeader('OrderNo', no)
    message.setHeader('Temp', null)
    messageLogFactory.getMessageLog(message)?.addCustomHeaderProperty('OrderNo', no)
    def pd = ITApiFactory.getService(PartnerDirectoryService, null)
    message.setProperty('Target', pd.getParameter('Target', message.getProperty('PID') as String, String))
    message.setBody("<Order><No>${no}</No></Order>")
    return message
}
