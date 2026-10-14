package com.sap.it.api;
import com.sap.it.api.pd.PartnerDirectoryService;
import java.util.HashMap;
import java.util.Map;
public class ITApiFactory {
    public static final Map<String, String> PD = new HashMap<>();
    @SuppressWarnings("unchecked")
    public static <T> T getService(Class<T> type, Object ctx) {
        return (T) (PartnerDirectoryService) new PartnerDirectoryService() {
            public <X> X getParameter(String name, String pid, Class<X> t) { return t.cast(PD.get(pid + "/" + name)); }
        };
    }
}
