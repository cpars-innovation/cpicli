import cpictl.standin.CaseContext
import cpictl.standin.MessageV1
import cpictl.standin.Services
import org.junit.jupiter.api.AfterEach
import org.junit.jupiter.api.Test
import static org.junit.jupiter.api.Assertions.*

class HeadersTest {
    @AfterEach
    void clear() { CaseContext.clear() }

    Headers script() {
        def s = new Headers()
        s.binding = new Binding(messageLogFactory: new Services.LogFactory())
        s
    }

    @Test
    void setsOrderHeaderAndTarget() {
        def ctx = CaseContext.builder().pd('PID1', 'Target', 'ERP-DEV').build().install()
        def msg = new MessageV1(headers: [OrderNo: '4711'], properties: [PID: 'PID1'])
        script().setOrderHeader(msg)
        assertEquals('ERP-DEV', msg.getProperty('Target'))
        assertEquals(['4711'], ctx.log.customHeaders['OrderNo'])
    }

    @Test
    void missingOrderNoFails() {
        assertThrows(IllegalStateException) { script().setOrderHeader(new MessageV1()) }
    }
}
