package cpictl.standin;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertSame;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.sap.gateway.ip.core.customdev.processor.SoapFaultFactory;
import com.sap.gateway.ip.core.customdev.processor.SoapFaultParam;
import com.sap.gateway.ip.core.customdev.processor.UnknownFaultCodeException;
import com.sap.gateway.ip.core.customdev.processor.UnknownSoapVersionException;
import com.sap.gateway.ip.core.customdev.processor.XiSoapFaultParam;
import com.sap.it.api.ITApi;
import com.sap.it.api.ITApiFactory;
import com.sap.it.api.asdk.datastore.DataBean;
import com.sap.it.api.asdk.datastore.DataConfig;
import com.sap.it.api.asdk.datastore.DataStoreException;
import com.sap.it.api.asdk.datastore.DataStoreService;
import com.sap.it.api.asdk.datastore.MetaData;
import com.sap.it.api.asdk.runtime.Factory;
import com.sap.it.api.exception.ITApiException;
import com.sap.it.api.exception.ITApiRuntimeException;
import com.sap.it.api.exception.InvalidContextException;
import com.sap.it.api.impl.ITApiFactoryRegistry;
import com.sap.it.api.keystore.KeystoreService;
import com.sap.it.api.keystore.exception.KeystoreException;
import com.sap.it.api.mapping.MappingContext;
import com.sap.it.api.mapping.ValueMappingApi;
import com.sap.it.api.msglog.MessageLog;
import com.sap.it.api.nrc.NumberRangeConfigurationService;
import com.sap.it.api.nrc.exception.NumberRangeConfigException;
import com.sap.it.api.pd.BinaryData;
import com.sap.it.api.pd.PartnerDirectoryService;
import com.sap.it.api.pd.exception.PartnerDirectoryException;
import com.sap.it.api.securestore.AccessTokenAndUser;
import com.sap.it.api.securestore.SecureStoreService;
import com.sap.it.api.securestore.UserCredential;
import com.sap.it.api.securestore.exception.CredentialNotFoundException;
import com.sap.it.api.securestore.exception.SecureStoreException;
import com.sap.it.spi.ITApiHandler;
import java.io.StringWriter;
import java.nio.charset.StandardCharsets;
import java.security.KeyPair;
import java.security.PrivateKey;
import java.security.cert.Certificate;
import java.security.cert.X509Certificate;
import java.util.Base64;
import java.util.Date;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import javax.xml.namespace.QName;
import org.bouncycastle.openssl.jcajce.JcaPEMWriter;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;

/** Conformance of every ITApiFactory service, the message log, mapping API, data store and SOAP faults. */
class ServicesConformanceTest {
    private static CaseContext context(Map<String, String> pd, Map<String, String> vm, Map<String, Map<String, String>> secure,
                                       Map<String, String> keystore) {
        CaseContext c = new CaseContext(pd, vm, secure, keystore);
        CaseContext.set(c);
        return c;
    }

    @AfterEach
    void clear() {
        CaseContext.clear();
    }

    @Test
    void messageLogRecordsEverything() {
        CaseContext c = context(null, null, null, null);
        MessageLog log = new Services.LogFactory().getMessageLog(new MessageV1());
        log.setStringProperty("s", "v");
        log.setIntegerProperty("i", 1);
        log.setLongProperty("l", 2L);
        log.setBooleanProperty("b", true);
        log.setFloatProperty("f", 1.5f);
        log.setDoubleProperty("d", 2.5);
        log.setDateProperty("t", new Date(0));
        log.addCustomHeaderProperty("OrderNo", "1");
        log.addCustomHeaderProperty("OrderNo", "2");
        log.addAttachmentAsString("payload", "<a/>", "text/xml");
        Map<?, ?> props = (Map<?, ?>) c.log.get("properties");
        assertEquals("v", props.get("s"));
        assertEquals(1, props.get("i"));
        assertEquals(2L, props.get("l"));
        assertEquals(true, props.get("b"));
        assertEquals(1.5f, props.get("f"));
        assertEquals(2.5, props.get("d"));
        assertEquals("1970-01-01T00:00:00.000Z", props.get("t"));
        assertEquals(List.of("1", "2"), ((Map<?, ?>) c.log.get("customHeaders")).get("OrderNo"));
        assertEquals("<a/>", ((Map<?, ?>) ((Map<?, ?>) c.log.get("attachments")).get("payload")).get("text"));
    }

    @Test
    void secureStoreAnswersFromTheCase() throws Exception {
        context(null, null, Map.of("SFTP", Map.of("user", "u", "password", "p"), "Token", Map.of("value", "t0k")), null);
        SecureStoreService s = ITApiFactory.getService(SecureStoreService.class, null);
        UserCredential c = s.getUserCredential("SFTP");
        assertEquals("u", c.getUsername());
        assertArrayEquals("p".toCharArray(), c.getPassword());
        assertTrue(c.getCredentialProperties().isEmpty());
        UserCredential param = s.getUserCredential("Token");
        assertArrayEquals("t0k".toCharArray(), param.getPassword(), "a secure parameter's value is its password");
        AccessTokenAndUser t = s.getAccesTokenForOauth2AuthorizationCodeCredential("Token");
        assertEquals("t0k", t.getAccessToken());
        assertNull(t.getUser());
        SecureStoreException e = assertThrows(CredentialNotFoundException.class, () -> s.getUserCredential("missing"));
        assertTrue(e.getMessage().contains("missing"));
    }

    @Test
    @SuppressWarnings("deprecation")
    void partnerDirectory() throws Exception {
        String xsl = "<xsl:stylesheet xmlns:xsl=\"http://www.w3.org/1999/XSL/Transform\" version=\"2.0\"/>";
        context(Map.of(
                "SAP_001/Mode", "test",
                "SAP_001/Mapping", Base64.getEncoder().encodeToString(xsl.getBytes(StandardCharsets.UTF_8)),
                "SAP_002/Mode", "prod",
                "SAP_001/@alt/SAP/ERP", "001",
                "SAP_001/@users", "alice, bob"), null, null, null);
        PartnerDirectoryService pd = ITApiFactory.getApi(PartnerDirectoryService.class, null);
        assertEquals("test", pd.getParameter("Mode", "SAP_001", String.class));
        BinaryData bin = pd.getParameter("Mapping", "SAP_001", BinaryData.class);
        assertEquals(xsl, new String(bin.getData(), StandardCharsets.UTF_8));
        assertEquals("xsl", bin.getContentType());
        assertNull(pd.getParameter("Nope", "SAP_001", String.class));
        assertThrows(PartnerDirectoryException.class, () -> pd.getParameter("Mode", "SAP_001", Integer.class));
        assertEquals("001", pd.getAlternativePartnerId("SAP", "ERP", "SAP_001"));
        assertEquals("SAP_001", pd.getPartnerId("SAP", "ERP", "001"));
        assertEquals(List.of("alice", "bob"), pd.getAuthorizedUsers("SAP_001"));
        assertEquals("SAP_001", pd.getPartnerIdOfAuthorizedUser("bob"));
        assertNull(pd.getPartnerIdOfAuthorizedUser("eve"));
        assertEquals(Map.of("SAP_001", "test", "SAP_002", "prod"), pd.getParameters("Mode", String.class));
        assertEquals(Map.of("SAP_002", "prod"), pd.getParametersMatchingPattern("%002", "Mode", String.class));
        assertEquals(java.util.Set.of("Mode", "Mapping"),
                java.util.Set.copyOf(pd.getParameterIdsMatchingPattern("SAP_001", "M*", String.class)));
        assertThrows(PartnerDirectoryException.class, () -> pd.getCertificateParameter("Mode", "SAP_001"));
        assertNull(pd.getCertificateParameter("Cert", "SAP_001"));
    }

    @Test
    void keystoreGeneratedAndPem() throws Exception {
        context(null, null, null, Map.of("gen", "generate"));
        KeystoreService ks = ITApiFactory.getService(KeystoreService.class, null);
        X509Certificate cert = (X509Certificate) ks.getCertificate("gen");
        assertTrue(cert.getSubjectX500Principal().getName().contains("CN=gen"));
        PrivateKey key = (PrivateKey) ks.getKey("gen");
        KeyPair kp = ks.getKeyPair("gen");
        assertEquals(cert.getPublicKey(), kp.getPublic());

        StringWriter pem = new StringWriter();
        try (JcaPEMWriter w = new JcaPEMWriter(pem)) {
            w.writeObject(cert);
            w.writeObject(key);
        }
        StringWriter certOnly = new StringWriter();
        try (JcaPEMWriter w = new JcaPEMWriter(certOnly)) {
            w.writeObject(cert);
        }
        context(Map.of("P/Cert", certOnly.toString()), null, null, Map.of("pem", pem.toString(), "trusted", certOnly.toString()));
        assertEquals(cert, ks.getCertificate("pem"));
        Certificate[] chain = ks.getCertificateChain("pem");
        assertEquals(1, chain.length);
        assertEquals(key, ks.getKey("pem"));
        assertNotNull(ks.getKeyPair("pem"));
        assertNull(ks.getKey("trusted"));
        assertThrows(KeystoreException.class, () -> ks.getKeyPair("trusted"));
        assertNotNull(ks.getKeyManager("pem"));
        assertEquals(1, ks.getKeyManagers("pem").length);
        assertNotNull(ks.getKeyManager());
        assertNotNull(ks.getKeyManagers());
        assertNotNull(ks.getTrustManager());
        assertNotNull(ks.getTrustManagers());
        assertThrows(KeystoreException.class, () -> ks.getCertificate("missing"));
        PartnerDirectoryService pd = ITApiFactory.getService(PartnerDirectoryService.class, null);
        assertEquals(cert, pd.getCertificateParameter("Cert", "P"));
    }

    @Test
    void valueMappingsAndNumberRanges() throws Exception {
        context(null, Map.of("S4|Plant|1000|CRM|Site", "Berlin"), null, null);
        ValueMappingApi vm = ITApiFactory.getApi(ValueMappingApi.class, null);
        assertEquals("Berlin", vm.getMappedValue("S4", "Plant", "1000", "CRM", "Site"));
        assertEquals(List.of("Berlin"), vm.getMappedValues("S4", "Plant", "1000", "CRM", "Site"));
        assertNull(vm.getMappedValue("S4", "Plant", "2000", "CRM", "Site"));
        assertEquals(List.of(), vm.getMappedValues("S4", "Plant", "2000", "CRM", "Site"));

        NumberRangeConfigurationService nr = ITApiFactory.getService(NumberRangeConfigurationService.class, null);
        assertEquals("1", nr.getNextValuefromNumberRange("Orders", null));
        assertEquals("2", nr.getNextValuefromNumberRange("Orders", null));
        assertThrows(NumberRangeConfigException.class, () -> nr.getNextValuefromNumberRange("", null));
    }

    @Test
    void dataStore() throws Exception {
        context(null, null, null, null);
        DataStoreService ds = new Factory(DataStoreService.class).getService(new HashMap<>());
        DataBean bean = new DataBean();
        bean.setDataAsArray("x".getBytes(StandardCharsets.UTF_8));
        bean.setHeaders(Map.of("h", "v"));
        DataConfig cfg = new DataConfig();
        cfg.setStoreName("Orders");
        cfg.setId("1");
        cfg.setQualifier(null);
        cfg.setExpires(30);
        cfg.setRetentionThreshold(2);
        MetaData md = ds.put(bean, cfg);
        assertEquals("Orders", md.getStoreName());
        assertEquals("1", md.getId());
        assertNull(md.getQualifier());
        assertTrue(md.getExpiresAt() > md.getCreatedAt());
        assertEquals(30, cfg.getExpires());
        assertEquals(2, cfg.getRetentionThreshold());
        assertFalse(cfg.isOverwrite());
        assertThrows(DataStoreException.class, () -> ds.put(bean, cfg));
        cfg.setOverwrite(true);
        ds.put(bean, cfg);
        DataBean got = ds.get("Orders", "1");
        assertArrayEquals("x".getBytes(StandardCharsets.UTF_8), got.getDataAsArray());
        assertEquals("v", got.getHeaders().get("h"));
        assertEquals("Orders", got.getMetaData().getStoreName());
        assertEquals(1, ds.select("Orders", 10).size());
        assertTrue(ds.delete("Orders", "1"));
        assertNull(ds.get("Orders", "1"));
        assertEquals(0, ds.select("Orders", 10).size());
        DataStoreService again = new Factory(DataStoreService.class).getService();
        assertNotNull(again);
        assertThrows(DataStoreException.class, () -> again.put(bean, new DataConfig()));
        new DataStoreException("m", new RuntimeException());
    }

    @Test
    void mappingContextAndOutput() {
        Map<String, Object> headers = new HashMap<>(Map.of("H", "1"));
        Map<String, Object> props = new HashMap<>(Map.of("P", 2));
        MappingContext ctx = new Services.Mapping(headers, props);
        assertEquals("1", ctx.getHeader("H"));
        assertEquals("2", ctx.getProperty("P"));
        ctx.setHeader("H2", "x");
        ctx.setProperty("P2", "y");
        assertEquals("x", headers.get("H2"));
        assertEquals("y", props.get("P2"));
        Services.Queue q = new Services.Queue();
        q.addValue("a");
        q.addSuppress();
        q.addContextChange();
        assertEquals(List.of("a", Services.Queue.SUPPRESS, Services.Queue.CONTEXT_CHANGE), q.values());
    }

    @Test
    void factoryRegistryAndUnsupportedServices() throws Exception {
        interface Custom extends ITApi {}
        Custom impl = new Custom() {};
        ITApiHandler<Custom> handler = ctx -> impl;
        ITApiFactoryRegistry reg = new ITApiFactoryRegistry();
        reg.bindApiFactory(handler, Map.of("api", Custom.class));
        assertSame(handler, ITApiFactoryRegistry.getApiHandler(Custom.class));
        assertSame(impl, ITApiFactory.getService(Custom.class, null));
        reg.unbindApiFactory(handler, Map.of());
        assertThrows(UnsupportedInStandInsException.class, () -> ITApiFactory.getService(Custom.class, null));
        assertInstanceOf(UnsupportedOperationException.class, new UnsupportedInStandInsException("x"));

        // the exception classes of the API
        assertEquals("m", new InvalidContextException("m").getMessage());
        assertNull(new InvalidContextException().getMessage());
        for (ITApiException e : new ITApiException[] {new ITApiException(), new ITApiException("m"),
                new ITApiException("m", new RuntimeException()), new ITApiException(new RuntimeException()),
                new KeystoreException(), new KeystoreException("m", new RuntimeException()), new KeystoreException(new RuntimeException()),
                new PartnerDirectoryException(), new PartnerDirectoryException("m"), new PartnerDirectoryException(new RuntimeException()),
                new PartnerDirectoryException("m", new RuntimeException()), new SecureStoreException(), new SecureStoreException("m"),
                new SecureStoreException("m", new RuntimeException()), new SecureStoreException(new RuntimeException()),
                new CredentialNotFoundException(), new CredentialNotFoundException("m", new RuntimeException()),
                new CredentialNotFoundException(new RuntimeException()), new NumberRangeConfigException("m")}) {
            assertNotNull(e);
        }
        for (ITApiRuntimeException e : new ITApiRuntimeException[] {new ITApiRuntimeException(), new ITApiRuntimeException("m"),
                new ITApiRuntimeException("m", new RuntimeException()), new ITApiRuntimeException(new RuntimeException())}) {
            assertNotNull(e);
        }
    }

    @Test
    void soapFaults() {
        SoapFaultParam p = new SoapFaultParam();
        p.setVersionNamespace(SoapFaultFactory.SOAP12_NAMESPACE);
        p.setFaultcode(new QName(SoapFaultFactory.SOAP12_NAMESPACE, "Sender"));
        p.setFaultstring("order rejected");
        p.setFaultactor("cpictl");
        p.setSubcodes(List.of(new QName("urn:x", "Invalid")));
        p.setNamespaces(Map.of("x", "urn:x"));
        p.setDetail(null);
        SoapFaultException f = assertThrows(SoapFaultException.class, () -> SoapFaultFactory.throwSoapFault(p));
        assertEquals("order rejected", f.getMessage());
        assertEquals("Sender", f.getFaultCode().getLocalPart());
        assertEquals("cpictl", f.getFaultActor());
        assertEquals(1, f.getSubcodes().size());
        assertNull(f.getDetail());
        assertEquals(Map.of("x", "urn:x"), p.getNamespaces());
        assertEquals("cpictl", p.getFaultactor());
        assertNull(p.getDetail());

        p.setFaultcode(new QName(SoapFaultFactory.SOAP12_NAMESPACE, "Client"));
        assertThrows(UnknownFaultCodeException.class, () -> SoapFaultFactory.throwSoapFault(p));
        p.setVersionNamespace("urn:soap99");
        assertThrows(UnknownSoapVersionException.class, () -> SoapFaultFactory.throwSoapFault(p));
        assertThrows(SoapFaultException.class, () -> SoapFaultFactory.throwSoapFault("plain", null, Map.of()));

        XiSoapFaultParam xi = new XiSoapFaultParam();
        xi.setFaultstring("xi fault");
        xi.setFaultactor("a");
        xi.setSapP1("1");
        xi.setSapP2("2");
        xi.setSapP3("3");
        xi.setSapP4("4");
        xi.setSapAdditionalText("t");
        xi.setSapStack("s");
        xi.setFaultcode(null);
        assertEquals("1234ts", xi.getSapP1() + xi.getSapP2() + xi.getSapP3() + xi.getSapP4() + xi.getSapAdditionalText() + xi.getSapStack());
        assertEquals("a", xi.getFaultactor());
        assertNull(xi.getFaultcode());
        SoapFaultException x = assertThrows(SoapFaultException.class, () -> SoapFaultFactory.throwXiSoapFault(xi));
        assertEquals("xi fault", x.getMessage());
        assertNotNull(new SoapFaultFactory());
    }

    @Test
    void builderSetsTheValuesTheServicesAnswer() throws Exception {
        CaseContext c = CaseContext.builder()
                .pd("PID", "Name", "v")
                .valueMapping("A", "I", "x", "B", "J", "y")
                .secure("alias", "user", "pw")
                .keystore("key", "generate")
                .build()
                .install();
        assertSame(c, CaseContext.current());
        assertEquals("v", ITApiFactory.getService(PartnerDirectoryService.class, null).getParameter("Name", "PID", String.class));
        assertEquals("y", ITApiFactory.getService(ValueMappingApi.class, null).getMappedValue("A", "I", "x", "B", "J"));
        UserCredential uc = ITApiFactory.getService(SecureStoreService.class, null).getUserCredential("alias");
        assertEquals("user", uc.getUsername());
        assertEquals("pw", new String(uc.getPassword()));
        assertNotNull(ITApiFactory.getService(KeystoreService.class, null).getCertificate("key"));
    }
}
