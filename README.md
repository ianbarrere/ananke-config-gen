# ananke-config-gen
This is a tool to generate OpenConfig serialized content for various configuration objects
based on Netbox definitions.

Its primary purpose is for use with Ananke, since it creates YAML files with the gNMI
path as the main key and commits them to a repo in the expected structure of the Ananke
repo, however the content could in theory be used for anything else OpenConfig related.

It can be thought of as something of a PoC since much of this configuration is very
bespoke and org-specific. As such, it was started to suit the needs of my org, and there
are probably a lot of random things that aren't supported for you out of the box. I
encourage you to submit PRs for functionality that you think would benefit the community,
and likewise you're welcome to customize extensively locally to suit your needs.

## Quick start
Minimally:

    ./ananke-config-gen <hostname1> ... <hostnameN>

None of the flags are required, and if given only one or more hostnames all config sections
will be generated for the hosts using an autogenerated branch name.

The --config-section/-c flag is used to indicate which types of config to generate. The
options are INTERFACES, OSPF, VLANS, LACP, ACL. These are additive, so to configure interfaces
and VLANs you can run:

    ./ananke-config-gen -c INTERFACES -c VLANS site1-switch01

The --filter/-f flag is only for use with interfaces, and allows you to generate only config
for certain interfaces or interface tags. These flags are additive, so you can specify
multiple:

    ./ananke-config-gen -f eth1/1 site1-switch01 site1-switch02
    ./ananke-config-gen -f ACCESS_PORT -f eth1/1 site1-switch01
    etc

**Note**: The -f flag is only really usable when using the default interface layout of
SEPARATE. This is because filtering interfaces only creates structures for those interfaces
matching the filter, thus overwriting the contents of the entire file if using the TOGETHER/
SAMEFILE interface layouts with only the specified interfaces.

The --explicit-descriptions/-D flag overrides the default behavior of autogenerating the
description fields and instead uses whatever description is set in the Description field
in Netbox.

The --output-format/-O flag allows you to specify either JSON or YAML, for your desired
serialization format.

The --stdout/-S flag allows you to print the output to stdout rather than committing to
a Gitlab repo.

The --interface-layout flag allows you to specify how you want the interfaces grouped. The
default is SEPARATE, meaning one interface per file, with a gNMI path specific to that
interface. If you want all interfaces under a single file and path, you can specify TOGETHER.
You can also specify SAMEFILE if you want individual gNMI paths per interface within a
single file.

**IMPORTANT** The flags *must* be given immediately after the command name otherwise they
will be silently ignored. This is due to the behavior of the CLI module in use.

## Technical Overview
The tool makes a main GraphQL query to Netbox in order to discover interface configs for
the given device(s). It makes several other calls to populate secondary data structures
like a mapping for VRRP groups and VLANs at a site.

## Repo Support
Without the -S flag, the tool will look to the environment variable ANANKE_REPO, which it
expects to be a Gitlab project ID. The environment variable ANANKE_REPO_PAT should be a
project access token with privileges to read and commit.

When the tool runs, it will create a new branch, commit the changes to the new branch,
create a PR towards main, and return the URL of the PR.

## Sections

### ACLs
(Add support for Netbox ACLs plugin)

### Interfaces

#### Description field
The interface description field is autogenerated to be informative. It attempts to gather
information from various attributes like VLAN membership, device on the other side of a
cable, etc. It adds the Description field from Netbox at the end as well. The -D flag
overrides this behavior and forces only the explicit description field.

##### Category Tags
You can optionally set an environment variable ANANKE_INTERFACE_CATEGORY_TAGS as a comma
separated list of interface tags from Netbox that will be added to the beginning of the
description field. E.g. let's say you tag your peering and transit interfaces with the tags
PEERING and TRANSIT. You can have these tags added to the description field by setting
the environment variable like so:

    ANANKE_INTERFACE_CATEGORY_TAGS="PEERING,TRANSIT"

The subsequent description field will have the tag at the beginning wrapped in square
brackets, e.g.

    description: '[PEERING] - Equinix NY IX edge-rtr01 - NYC17-999043:EQIX:2120947-A'

This is sometimes helpful for interface classification in external tools like Kentik, etc.

#### Subinterfaces
Subinterfaces are associated to their parent interface via the Parent field in Netbox. Make
sure this field is set correctly in order for subinterfaces to show up under the correct
parent.

##### VLAN ID of a Subinterface
A subinterface's 802.1Q Mode must be set to "Access" with an untagged VLAN, and that VLAN
will be set as the VLAN ID in the OpenConfig schema.

##### Unsupported options
Since subinterfaces are modeled as full interfaces in Netbox, those fields that are not
compatible (duplex, speed, other physical characteristics) are ignored.

#### SVIs
An SVI's type must be set as Virtual in Netbox, and while not strictly necessary in some
cases, in order to unambiguously associate an SVI with its VLAN its 802.1Q Mode must be
set to Access, and the Untagged VLAN set accordingly. SVIs are currently indicated by
looking for the string "vlan" in the interface name, which is rather clunky, but works
with Cisco for the most part. Let me know of how other vendors do it and we can improve.

#### Port channels
A port channel interface should be created with type "Link Aggregation Group (LAG)". For
a member interface, set the LAG field to the port channel interface. **All other settings
for the physical interface should be left blank**.

### LACP
This is entirely derived from the interface list, and simply populates the openconfig:/lacp
structure based on port channel groups configured.

### OSPF
There is little to describe in terms of OSPF in Netbox, so the tool simply looks for
interfaces with the tag OSPF_PASSIVE or OSPF_ACTIVE, and assigns those into area 0.

### VLANs
Also not very sophisticated, but this option builds the VLAN table based on VLANs that
are present at the same site as the device. All that is configured is the VLAN ID and then
a name, which is roughly how it's represented in Netbox but with a few illegal character
types smoothed over (and converted to all caps). This is generally only required for
switches, and can probably be ignored for layer 3 devices in most cases.
