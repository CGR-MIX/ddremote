//go:build android

#include <jni.h>
#include <stdlib.h>
#include <string.h>
extern char *DDCall(char *, char *);

JNIEXPORT jbyteArray JNICALL Java_com_limelight_ddremote_EmbeddedNetwork_nativeCall(
    JNIEnv *env, jclass cls, jbyteArray method, jbyteArray payload) {
    jsize ml = (*env)->GetArrayLength(env, method), pl = (*env)->GetArrayLength(env, payload);
    char *m = calloc((size_t)ml+1,1), *p = calloc((size_t)pl+1,1);
    if (!m || !p) {free(m);free(p);return NULL;}
    (*env)->GetByteArrayRegion(env, method, 0, ml, (jbyte *)m);
    (*env)->GetByteArrayRegion(env, payload, 0, pl, (jbyte *)p);
    if ((*env)->ExceptionCheck(env)) {free(m);free(p);return NULL;}
    char *result = DDCall(m,p);
    free(m);free(p);
    size_t len = strlen(result);
    jbyteArray output = (*env)->NewByteArray(env,(jsize)len);
    if (output) (*env)->SetByteArrayRegion(env,output,0,(jsize)len,(jbyte *)result);
    free(result);
    return output;
}
