#!/opt/zextras/bin/zmpython
# SPDX-FileCopyrightText: 2022 Synacor, Inc.
# SPDX-FileCopyrightText: 2022 Zextras <https://www.zextras.com>
# SPDX-FileCopyrightText: 2026 Zextras <https://www.zextras.com>
#
# SPDX-License-Identifier: GPL-2.0-only
#
# Runs the unmodified legacy zmconfigd main-loop body for several consecutive
# cycles in ONE process, so the state the daemon keeps between cycles (previous
# keys, previous services, restart bookkeeping) is the real one. The body is
# copied from /opt/zextras/libexec/zmconfigd; what is left out is the process
# plumbing that is not part of a cycle: signal handlers, the request listener,
# the watchdog (it needs live services) and the sleep between cycles.
#
# Usage: legacy_cycles.py <case-name> <cycle-count>
# Before each cycle "cycle.sh prepare N" applies that cycle's inputs, after it
# "cycle.sh collect N" records the outputs.
import os
import sys
import tempfile
import threading
import traceback

tempfile.tempdir = "/opt/zextras/data/tmp"

import conf
from org.apache.logging.log4j.core.config import Configurator
import state
from ldap import Ldap
from logmsg import *

case = sys.argv[1]
cycles = int(sys.argv[2])


def hook(what, n):
	if os.system("/golden/bin/cycle.sh %s %s %d" % (what, case, n)) != 0:
		Log.logMsg(0, "cycle hook %s %d failed" % (what, n))


# The first cycle's inputs (localconfig.xml, directory) must exist before the
# daemon state is created, exactly as at daemon start.
hook("prepare", 1)

myState = state.State()
state.State.mState = myState
myConfig = conf.Config()
conf.Config.mConfig = myConfig
myState.getLocalConfig(myConfig)
Configurator.initialize(None, "/opt/zextras/conf/zmconfigd.log4j.properties")
Log.initLogging(myConfig)
Ldap.initLdap(myConfig)

for n in range(1, cycles + 1):
	if n > 1:
		hook("prepare", n)
		# The daemon re-reads localconfig on every cycle (getAllConfigs).

	myState.getAllConfigs(myConfig)
	myState.getMtaConfig(myConfig.configFile)
	myState.compareKeys()

	myState.lAction.acquire()
	try:
		myState.compileActions()
		myState.requestedconfig = {}
		myState.doConfigRewrites()
	finally:
		myState.lAction.notifyAll()
		myState.lAction.release()

	if myConfig.restartconfig:
		myState.doRestarts()

	myState.firstRun = False

	for th in threading.enumerate():
		if th.getName() != "MainThread" and not th.getName().startswith("Thread"):
			th.join(5)

	hook("collect", n)

sys.exit(0)
