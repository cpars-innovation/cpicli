package cpictl.standin;

import com.sap.it.api.keystore.KeystoreService;
import com.sap.it.api.keystore.exception.KeystoreException;
import java.io.StringReader;
import java.math.BigInteger;
import java.security.Key;
import java.security.KeyPair;
import java.security.KeyPairGenerator;
import java.security.KeyStore;
import java.security.PrivateKey;
import java.security.cert.Certificate;
import java.security.cert.X509Certificate;
import java.util.ArrayList;
import java.util.Date;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import javax.net.ssl.KeyManager;
import javax.net.ssl.KeyManagerFactory;
import javax.net.ssl.TrustManager;
import javax.net.ssl.TrustManagerFactory;
import org.bouncycastle.asn1.pkcs.PrivateKeyInfo;
import org.bouncycastle.asn1.x500.X500Name;
import org.bouncycastle.cert.X509CertificateHolder;
import org.bouncycastle.cert.jcajce.JcaX509CertificateConverter;
import org.bouncycastle.cert.jcajce.JcaX509v3CertificateBuilder;
import org.bouncycastle.openssl.PEMKeyPair;
import org.bouncycastle.openssl.PEMParser;
import org.bouncycastle.openssl.jcajce.JcaPEMKeyConverter;
import org.bouncycastle.operator.jcajce.JcaContentSignerBuilder;

/**
 * The keystore of a case: alias to PEM (certificates and an optional private key, PKCS#1, PKCS#8 or
 * SEC1) or "generate" for a fresh RSA key with a self-signed certificate. Never a tenant's keys.
 */
final class Keystore implements KeystoreService {
    private static final char[] NO_PASSWORD = new char[0];
    /** Generated test keys, per alias and JVM (one job): stable within the job. */
    private static final Map<String, Entry> GENERATED = new ConcurrentHashMap<>();

    record Entry(PrivateKey key, List<X509Certificate> chain) {}

    static Entry entry(String alias) throws KeystoreException {
        String pem = CaseContext.current().keystore.get(alias);
        if (pem == null) {
            throw new KeystoreException("no keystore entry '" + alias + "' in the test case (keystore:)");
        }
        if (pem.trim().equals("generate")) {
            try {
                return GENERATED.computeIfAbsent(alias, Keystore::generate);
            } catch (IllegalStateException e) {
                throw new KeystoreException(e.getMessage(), e);
            }
        }
        try (PEMParser p = new PEMParser(new StringReader(pem))) {
            JcaPEMKeyConverter keys = new JcaPEMKeyConverter();
            JcaX509CertificateConverter certs = new JcaX509CertificateConverter();
            PrivateKey key = null;
            List<X509Certificate> chain = new ArrayList<>();
            for (Object o = p.readObject(); o != null; o = p.readObject()) {
                if (o instanceof X509CertificateHolder h) {
                    chain.add(certs.getCertificate(h));
                } else if (o instanceof PEMKeyPair kp) {
                    key = keys.getKeyPair(kp).getPrivate();
                } else if (o instanceof PrivateKeyInfo pk) {
                    key = keys.getPrivateKey(pk);
                }
            }
            if (key == null && chain.isEmpty()) {
                throw new KeystoreException("keystore entry '" + alias + "' holds no certificate or key in PEM form");
            }
            return new Entry(key, chain);
        } catch (KeystoreException e) {
            throw e;
        } catch (Exception e) {
            throw new KeystoreException("keystore entry '" + alias + "': " + e.getMessage(), e);
        }
    }

    private static Entry generate(String alias) {
        try {
            KeyPairGenerator g = KeyPairGenerator.getInstance("RSA");
            g.initialize(2048);
            KeyPair kp = g.generateKeyPair();
            X500Name name = new X500Name("CN=" + alias.replaceAll("[,=+<>#;\"\\\\]", "_") + ", O=cpictl test key");
            Date from = new Date(System.currentTimeMillis() - 60_000);
            Date to = new Date(System.currentTimeMillis() + 365L * 86_400_000L);
            JcaX509v3CertificateBuilder b = new JcaX509v3CertificateBuilder(name, BigInteger.valueOf(System.nanoTime()), from, to,
                    name, kp.getPublic());
            X509Certificate cert = new JcaX509CertificateConverter()
                    .getCertificate(b.build(new JcaContentSignerBuilder("SHA256withRSA").build(kp.getPrivate())));
            return new Entry(kp.getPrivate(), List.of(cert));
        } catch (Exception e) {
            throw new IllegalStateException("generating a test key for '" + alias + "': " + e.getMessage(), e);
        }
    }

    private static KeyStore store(Iterable<String> aliases, boolean keysOnly) throws KeystoreException {
        try {
            KeyStore ks = KeyStore.getInstance("PKCS12");
            ks.load(null, null);
            for (String alias : aliases) {
                Entry e = entry(alias);
                if (e.key() != null && !e.chain().isEmpty()) {
                    ks.setKeyEntry(alias, e.key(), NO_PASSWORD, e.chain().toArray(new Certificate[0]));
                } else if (!keysOnly && !e.chain().isEmpty()) {
                    ks.setCertificateEntry(alias, e.chain().get(0));
                }
            }
            return ks;
        } catch (KeystoreException e) {
            throw e;
        } catch (Exception e) {
            throw new KeystoreException(e.getMessage(), e);
        }
    }

    @Override
    public Certificate getCertificate(String alias) throws KeystoreException {
        List<X509Certificate> chain = entry(alias).chain();
        return chain.isEmpty() ? null : chain.get(0);
    }

    @Override
    public Certificate[] getCertificateChain(String alias) throws KeystoreException {
        return entry(alias).chain().toArray(new Certificate[0]);
    }

    @Override
    public Key getKey(String alias) throws KeystoreException {
        return entry(alias).key();
    }

    @Override
    public KeyPair getKeyPair(String alias) throws KeystoreException {
        Entry e = entry(alias);
        if (e.key() == null || e.chain().isEmpty()) {
            throw new KeystoreException("keystore entry '" + alias + "' has no key pair (private key and certificate)");
        }
        return new KeyPair(e.chain().get(0).getPublicKey(), e.key());
    }

    @Override
    public KeyManager getKeyManager() throws KeystoreException {
        KeyManager[] m = getKeyManagers();
        return m.length == 0 ? null : m[0];
    }

    @Override
    public KeyManager getKeyManager(String alias) throws KeystoreException {
        KeyManager[] m = getKeyManagers(alias);
        return m.length == 0 ? null : m[0];
    }

    @Override
    public KeyManager[] getKeyManagers() throws KeystoreException {
        return keyManagers(CaseContext.current().keystore.keySet());
    }

    @Override
    public KeyManager[] getKeyManagers(String alias) throws KeystoreException {
        return keyManagers(List.of(alias));
    }

    private static KeyManager[] keyManagers(Iterable<String> aliases) throws KeystoreException {
        try {
            KeyManagerFactory f = KeyManagerFactory.getInstance(KeyManagerFactory.getDefaultAlgorithm());
            f.init(store(aliases, true), NO_PASSWORD);
            return f.getKeyManagers();
        } catch (KeystoreException e) {
            throw e;
        } catch (Exception e) {
            throw new KeystoreException(e.getMessage(), e);
        }
    }

    @Override
    public TrustManager getTrustManager() throws KeystoreException {
        TrustManager[] m = getTrustManagers();
        return m.length == 0 ? null : m[0];
    }

    @Override
    public TrustManager[] getTrustManagers() throws KeystoreException {
        try {
            TrustManagerFactory f = TrustManagerFactory.getInstance(TrustManagerFactory.getDefaultAlgorithm());
            KeyStore ks = KeyStore.getInstance("PKCS12");
            ks.load(null, null);
            for (String alias : CaseContext.current().keystore.keySet()) {
                int i = 0;
                for (X509Certificate c : entry(alias).chain()) {
                    ks.setCertificateEntry(alias + "#" + i++, c);
                }
            }
            f.init(ks);
            return f.getTrustManagers();
        } catch (KeystoreException e) {
            throw e;
        } catch (Exception e) {
            throw new KeystoreException(e.getMessage(), e);
        }
    }
}
