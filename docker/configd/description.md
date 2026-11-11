Carbonio configd: the configuration daemon and service control CLI of Carbonio, a drop-in replacement for the legacy zmconfigd.

It renders the Carbonio service configuration (Postfix, amavis, OpenDKIM, nginx proxy, ...) from LDAP and localconfig through the zmconfigd.cf templates, and restarts or reloads the affected services when values change.

The image is built FROM scratch and contains only:
- /opt/zextras/bin/configd: static binary (entrypoint)
- /opt/zextras/share/configd: zmconfigd.cf, the conf/*.in templates and the LDAP attrs schema from the carbonio-core and carbonio-common-appserver-conf packages of the same release
- /opt/zextras/libexec/carbonio-postfix-conf: the root helper that applies Postfix configuration changes (/opt/zextras/common/conf is root-owned, configd never writes it), and its staging directory /opt/zextras/data/postfix/conf-staging
- a zextras user (uid/gid 999), which the container runs as

Use it as a container running configd alone, or as a layer:
COPY --from=<registry>/carbonio-configd:<tag> /opt/zextras/ /opt/zextras/

Postfix configuration: configd stages the change and runs the helper directly when it is root, or through sudo -n otherwise. The image has no sudo and runs as uid 999, so a Postfix configuration change fails with an error naming the helper; run the container as root if configd must manage Postfix configuration.

Environment: CARBONIO_BASE_DIR=/opt/zextras, CONFIGD_LC_zmconfigd_template_dir=/opt/zextras/share/configd. Any localconfig key can be overridden with CONFIGD_LC_<key>.

Documentation: https://github.com/zextras/carbonio-configd/tree/main/docs
