package cpictl.standin;

/**
 * A call the stand-ins cannot answer outside CPI (a service, a conversion or an expression they do
 * not implement). It is not a failure of the script: test that part on the tenant.
 */
public class UnsupportedInStandInsException extends UnsupportedOperationException {
    private static final long serialVersionUID = 1L;

    public UnsupportedInStandInsException(String what) {
        super(what + " is not available in the script test stand-ins: test this on the tenant");
    }
}
