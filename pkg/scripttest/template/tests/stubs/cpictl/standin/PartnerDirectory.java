package cpictl.standin;

import com.sap.it.api.pd.BinaryData;
import com.sap.it.api.pd.PartnerDirectoryService;
import com.sap.it.api.pd.exception.PartnerDirectoryException;
import java.io.ByteArrayInputStream;
import java.nio.charset.StandardCharsets;
import java.security.cert.CertificateFactory;
import java.security.cert.X509Certificate;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Pattern;

/**
 * The Partner Directory of a case. Keys of the case's {@code pd}:
 * <ul>
 *   <li>{@code <pid>/<param>}: a parameter (binary values base64; String values as they are)</li>
 *   <li>{@code <pid>/@alt/<agency>/<scheme>}: the partner's alternative partner id</li>
 *   <li>{@code <pid>/@users}: authorized users, comma-separated</li>
 * </ul>
 */
final class PartnerDirectory implements PartnerDirectoryService {
    private static final String ALT = "/@alt/";
    private static final String USERS = "/@users";

    private static Map<String, String> pd() {
        return CaseContext.current().pd;
    }

    /** Decodes a binary value: base64 as the contract says; a value that is not base64 is taken as text. */
    static byte[] binary(String v) {
        try {
            return Base64.getMimeDecoder().decode(v.trim());
        } catch (IllegalArgumentException e) {
            return v.getBytes(StandardCharsets.UTF_8);
        }
    }

    static String contentType(byte[] data) {
        String head = new String(data, 0, Math.min(data.length, 512), StandardCharsets.UTF_8).trim();
        if (head.contains("XSL/Transform")) return "xsl";
        if (head.startsWith("<")) return head.contains("XMLSchema") ? "xsd" : "xml";
        if (head.startsWith("{") || head.startsWith("[")) return "json";
        if (head.startsWith("-----BEGIN CERTIFICATE")) return "crt";
        if (data.length > 1 && data[0] == 'P' && data[1] == 'K') return "zip";
        return "octet-stream";
    }

    private static <T> T convert(String v, Class<T> type) throws PartnerDirectoryException {
        if (v == null) {
            return null;
        }
        if (type == String.class) {
            return type.cast(v);
        }
        if (type == BinaryData.class) {
            byte[] data = binary(v);
            String ct = contentType(data);
            return type.cast(new BinaryData() {
                @Override
                public String getContentType() {
                    return ct;
                }

                @Override
                public byte[] getData() {
                    return data.clone();
                }
            });
        }
        throw new PartnerDirectoryException("unsupported parameter type " + (type == null ? "null" : type.getName())
                + " (String or BinaryData)");
    }

    @Override
    public <T> T getParameter(String id, String pid, Class<T> type) throws PartnerDirectoryException {
        return convert(pd().get(pid + "/" + id), type);
    }

    @Override
    public X509Certificate getCertificateParameter(String id, String pid) throws PartnerDirectoryException {
        String v = pd().get(pid + "/" + id);
        if (v == null) {
            return null;
        }
        try {
            byte[] data = v.contains("-----BEGIN") ? v.getBytes(StandardCharsets.US_ASCII) : binary(v);
            return (X509Certificate) CertificateFactory.getInstance("X.509").generateCertificate(new ByteArrayInputStream(data));
        } catch (Exception e) {
            throw new PartnerDirectoryException("parameter " + pid + "/" + id + " is not a certificate: " + e.getMessage(), e);
        }
    }

    @Override
    public String getAlternativePartnerId(String agency, String scheme, String pid) {
        return pd().get(pid + ALT + agency + "/" + scheme);
    }

    @Override
    public String getPartnerId(String agency, String scheme, String alternativePartnerId) {
        String suffix = ALT + agency + "/" + scheme;
        for (Map.Entry<String, String> e : pd().entrySet()) {
            if (e.getKey().endsWith(suffix) && e.getValue().equals(alternativePartnerId)) {
                return e.getKey().substring(0, e.getKey().length() - suffix.length());
            }
        }
        return null;
    }

    @Override
    public List<String> getAuthorizedUsers(String pid) {
        String v = pd().get(pid + USERS);
        List<String> out = new ArrayList<>();
        if (v != null) {
            for (String u : v.split(",")) {
                if (!u.isBlank()) {
                    out.add(u.trim());
                }
            }
        }
        return out;
    }

    @Override
    public String getPartnerIdOfAuthorizedUser(String user) {
        for (Map.Entry<String, String> e : pd().entrySet()) {
            if (e.getKey().endsWith(USERS)) {
                String pid = e.getKey().substring(0, e.getKey().length() - USERS.length());
                if (getAuthorizedUsers(pid).contains(user)) {
                    return pid;
                }
            }
        }
        return null;
    }

    /** Parameters only (not the @alt / @users keys): pid to param id to value. */
    private static Map<String, Map<String, String>> params() {
        Map<String, Map<String, String>> out = new LinkedHashMap<>();
        for (Map.Entry<String, String> e : pd().entrySet()) {
            int i = e.getKey().indexOf('/');
            if (i <= 0 || e.getKey().contains("/@")) {
                continue;
            }
            out.computeIfAbsent(e.getKey().substring(0, i), k -> new LinkedHashMap<>()).put(e.getKey().substring(i + 1), e.getValue());
        }
        return out;
    }

    /** SAP's patterns: '*' or '%' for any characters. */
    private static Pattern glob(String p) {
        StringBuilder b = new StringBuilder();
        for (char c : (p == null ? "*" : p).toCharArray()) {
            b.append(c == '*' || c == '%' ? ".*" : Pattern.quote(String.valueOf(c)));
        }
        return Pattern.compile(b.toString());
    }

    @Override
    @Deprecated
    public List<String> getParameterIdsMatchingPattern(String pidPattern, String idPattern, Class<?> type) {
        Pattern pp = glob(pidPattern);
        Pattern ip = glob(idPattern);
        List<String> out = new ArrayList<>();
        params().forEach((pid, ps) -> {
            if (pp.matcher(pid).matches()) {
                ps.keySet().stream().filter(id -> ip.matcher(id).matches() && !out.contains(id)).forEach(out::add);
            }
        });
        return out;
    }

    @Override
    @Deprecated
    public <T> Map<String, T> getParameters(String id, Class<T> type) throws PartnerDirectoryException {
        return getParametersMatchingPattern("*", id, type);
    }

    @Override
    @Deprecated
    public <T> Map<String, T> getParametersMatchingPattern(String pidPattern, String id, Class<T> type)
            throws PartnerDirectoryException {
        Pattern pp = glob(pidPattern);
        Map<String, T> out = new LinkedHashMap<>();
        for (Map.Entry<String, Map<String, String>> e : params().entrySet()) {
            String v = e.getValue().get(id);
            if (v != null && pp.matcher(e.getKey()).matches()) {
                out.put(e.getKey(), convert(v, type));
            }
        }
        return out;
    }
}
