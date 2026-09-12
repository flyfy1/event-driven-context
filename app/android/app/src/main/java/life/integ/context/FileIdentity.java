package life.integ.context;

import java.io.File;
import java.io.FileInputStream;
import java.io.IOException;
import java.security.MessageDigest;
import java.security.NoSuchAlgorithmException;

final class FileIdentity {
    final long size;
    final String sha256;

    private FileIdentity(long size, String sha256) {
        this.size = size;
        this.sha256 = sha256;
    }

    static FileIdentity read(File file) throws IOException {
        if (!file.isFile() || file.length() <= 0) throw new IOException("capture file is empty or missing");
        try {
            MessageDigest digest = MessageDigest.getInstance("SHA-256");
            long count = 0;
            byte[] buffer = new byte[64 * 1024];
            try (FileInputStream input = new FileInputStream(file)) {
                int read;
                while ((read = input.read(buffer)) != -1) {
                    digest.update(buffer, 0, read);
                    count += read;
                }
            }
            return new FileIdentity(count, hex(digest.digest()));
        } catch (NoSuchAlgorithmException impossible) {
            throw new AssertionError(impossible);
        }
    }

    private static String hex(byte[] value) {
        StringBuilder result = new StringBuilder(value.length * 2);
        for (byte b : value) result.append(String.format("%02x", b & 0xff));
        return result.toString();
    }
}
