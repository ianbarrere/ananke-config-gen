package netbox

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	ocacl "github.com/ibarrere/ananke-config-gen/pkg/bindings/OcAcl"
	ocinterfaces "github.com/ibarrere/ananke-config-gen/pkg/bindings/OcInterfaces"
	oclacp "github.com/ibarrere/ananke-config-gen/pkg/bindings/OcLacp"
	ocnetinst "github.com/ibarrere/ananke-config-gen/pkg/bindings/ocnetinst"
	"github.com/ibarrere/ananke-config-gen/pkg/repo"
	"github.com/ibarrere/ananke-config-gen/pkg/repoconfig"
	"github.com/ibarrere/ananke-config-gen/pkg/repofile"
	"github.com/openconfig/ygot/ygot"
)

func NewNetboxApi() NetboxApi {
	return NetboxApi{
		Url: "https://netbox.doubleverify.prod",
		Client: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}
}

type NetboxApi struct {
	Url    string
	Client *http.Client
}

func (n NetboxApi) Request(method string, suffix string, body []byte) *http.Response {
	url := n.Url + "/" + suffix
	req, err := http.NewRequest(
		method,
		url,
		bytes.NewBuffer(body),
	)
	if err != nil {
		fmt.Println(err)
	}
	req.Header = http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Token " + os.Getenv("NETBOX_API_TOKEN")},
	}
	resp, err := n.Client.Do(req)
	statusOK := resp.StatusCode >= 200 && resp.StatusCode < 300
	if !statusOK {
		responseBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			fmt.Println(readErr)
		}
		fmt.Println(string(url))
		fmt.Println(string(responseBody))
	}
	if err != nil {
		fmt.Println(err)
	}
	return resp
}

func (cfn ConfigFromNetbox) GetRepoConfig(repo repo.GitlabRepo, interfaceLayout string) map[string][]repofile.RepoFile {
	// Get RepoConfig objects from ConfigFromNetbox data.
	configFiles := map[string][]repofile.RepoFile{}
	for device, deviceConfigObjects := range cfn.DeviceObjects {
		filePrefix := repo.GetHostPrefix(device)
		if interfaceLayout == "SEPARATE" {
			for intName, intConfigSect := range deviceConfigObjects.Interfaces {
				filePathIntId := strings.Replace(intName, "/", "-", -1)
				filePath := filePrefix + fmt.Sprintf("/interfaces-%s.yaml.j2", filePathIntId)
				intConfigSect.Path = "openconfig:/interfaces/interface[name=" + intName + "]"
				configFiles[device] = append(configFiles[device],
					repofile.NewRepoFile(filePath, []repoconfig.RepoConfig{intConfigSect}))
			}
		} else if interfaceLayout == "SAMEFILE" {
			filePath := filePrefix + "/interfaces.yaml.j2"
			repoConfigs := []repoconfig.RepoConfig{}
			for intName, intConfigSect := range deviceConfigObjects.Interfaces {
				intConfigSect.Path = "openconfig:/interfaces/interface[name=" + intName + "]"
				repoConfigs = append(repoConfigs, intConfigSect)
			}
			repoFile := repofile.NewRepoFile(filePath, repoConfigs)
			configFiles[device] = append(configFiles[device], repoFile)
		} else if interfaceLayout == "TOGETHER" {
			filePath := filePrefix + "/interfaces.yaml.j2"
			interfaces := &ocinterfaces.Device{
				Interfaces: &ocinterfaces.OpenconfigInterfaces_Interfaces{
					Interface: map[string]*ocinterfaces.OpenconfigInterfaces_Interfaces_Interface{},
				},
			}
			for intName, intConfigSect := range deviceConfigObjects.Interfaces {
				interfaces.Interfaces.Interface[intName] = intConfigSect.Binding.(*ocinterfaces.OpenconfigInterfaces_Interfaces_Interface)
			}
			repoFile := repofile.RepoFile{
				FilePath: filePath,
				ConfigSections: []repoconfig.RepoConfig{
					{
						Path:    "openconfig:/interfaces",
						Binding: interfaces.Interfaces,
					},
				},
			}
			configFiles[device] = append(configFiles[device], repoFile)
		}
		if deviceConfigObjects.Acl.Binding != nil {
			filePath := filePrefix + "/acl.yaml.j2"
			deviceConfigObjects.Acl.Path = "openconfig:/acl/interfaces"
			configFiles[device] = append(configFiles[device],
				repofile.NewRepoFile(filePath, []repoconfig.RepoConfig{deviceConfigObjects.Acl}))
		}
		if deviceConfigObjects.Vlans.Binding != nil {
			filePath := filePrefix + "/vlan.yaml.j2"
			deviceConfigObjects.Vlans.Path = "openconfig:/network-instance[name=DEFAULT]/vlans"
			configFiles[device] = append(configFiles[device],
				repofile.NewRepoFile(filePath, []repoconfig.RepoConfig{deviceConfigObjects.Vlans}))
		}
		if deviceConfigObjects.Ospf.Binding != nil {
			filePath := filePrefix + "/ospfv2.yaml.j2"
			deviceConfigObjects.Ospf.Path = "openconfig:/network-instance[name=DEFAULT]/protocols/protocol[name=OSPF]/ospfv2"
			configFiles[device] = append(configFiles[device],
				repofile.NewRepoFile(filePath, []repoconfig.RepoConfig{deviceConfigObjects.Ospf}))
		}
		if deviceConfigObjects.Lacp.Binding != nil {
			filePath := filePrefix + "/lacp.yaml.j2"
			deviceConfigObjects.Lacp.Path = "openconfig:/lacp"
			configFiles[device] = append(configFiles[device],
				repofile.NewRepoFile(filePath, []repoconfig.RepoConfig{deviceConfigObjects.Lacp}))
		}
	}
	return configFiles
}

func NewConfigFromNetbox(deviceNames []string) ConfigFromNetbox {
	// Initialize and return ConfigFromNetbox object
	cfn := ConfigFromNetbox{Api: NewNetboxApi()}
	cfn.QueryInterfaces(deviceNames)
	cfn.GetConnectedInterfaces()
	cfn.DeviceObjects = make(map[string]CfnObjects)
	for _, device := range deviceNames {
		cfn.DeviceObjects[device] = CfnObjects{}
	}
	return cfn
}

type ConfigFromNetbox struct {
	Devices             []string
	Api                 NetboxApi
	ConnectedInterfaces map[int]ConnectedInterface
	InterfaceMapping    map[string]InterfaceMapping
	DeviceObjects       map[string]CfnObjects
}

type InterfaceMapping struct {
	Device              InterfaceQueryDevice
	Interfaces          map[string]InterfaceQueryInterface
	SubinterfaceMapping map[string][]string
}

type InterfaceQueryResponse struct {
	Data struct {
		DeviceList []InterfaceQueryDevice `json:"device_list"`
	} `json:"data"`
}

type InterfaceQueryDevice struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Platform struct {
		Slug string `json:"slug"`
	} `json:"platform"`
	Site struct {
		Slug string `json:"slug"`
	} `json:"site"`
	Interfaces []InterfaceQueryInterface `json:"interfaces"`
}

type InterfaceQueryInterface struct {
	Tags []struct {
		Name string `json:"name"`
	} `json:"tags"`
	Vrf struct {
		Name string `json:"name"`
	} `json:"vrf"`
	MemberInterfaces []struct {
		Name string `json:"name"`
		Id   string `json:"id"`
	} `json:"member_interfaces"`
	Id          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	MgmtOnly    bool   `json:"mgmt_only"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
	Label       string `json:"label"`
	Mtu         int    `json:"mtu"`
	MacAddress  string `json:"mac_address"`
	Lag         struct {
		Name string `json:"name"`
	} `json:"lag"`
	Speed  int    `json:"speed"`
	Duplex string `json:"duplex"`
	Mode   string `json:"mode"`
	Parent struct {
		Name string `json:"name"`
	} `json:"parent"`
	TaggedVlans []struct {
		Vid int `json:"vid"`
	} `json:"tagged_vlans"`
	UntaggedVlan struct {
		Vid  int    `json:"vid"`
		Name string `json:"name"`
		Tags []struct {
			Name string `json:"name"`
		} `json:"tags"`
	} `json:"untagged_vlan"`
	IpAddresses []struct {
		Id          string `json:"id"`
		Address     string `json:"address"`
		Description string `json:"description"`
	} `json:"ip_addresses"`
	Cable struct {
		Id string `json:"id"`
	} `json:"cable"`
}

type ConnectedInterfaces struct {
	Results []ConnectedInterface `json:"results"`
}

type ConnectedInterface struct {
	Id    int `json:"id"`
	Cable struct {
		Id int `json:"id"`
	} `json:"cable"`
	ConnectedEndpoints []struct {
		Id     int    `json:"id"`
		Name   string `json:"name"`
		Device struct {
			Id   int    `json:"id"`
			Name string `json:"name"`
		} `json:"device"`
		Cable int `json:"cable"`
	} `json:"connected_endpoints"`
}

func (cfn *ConfigFromNetbox) GetConnectedInterfaces() {
	// Get connected interfaces from Netbox, these are required to build interface
	// descriptions
	if len(cfn.InterfaceMapping) == 0 {
		panic("Nothing in DeviceList, have you run GetInterfaces?")
	}
	devices := []string{}
	for hostname := range cfn.InterfaceMapping {
		devices = append(devices, hostname)
	}
	connectedInterfaceMapping := make(map[int]ConnectedInterface)
	urlBase := "api/dcim/interfaces"
	var url string
	if len(devices) > 1 {
		url = urlBase + "?device=" + strings.Join(devices, "&device=")
	} else if len(devices) == 1 {
		url = urlBase + "?device=" + devices[0]
	} else {
		cfn.ConnectedInterfaces = connectedInterfaceMapping
		return
	}
	resp := cfn.Api.Request("GET", url, []byte(""))
	body, _ := io.ReadAll(resp.Body)
	var connectedInterfaces ConnectedInterfaces
	err := json.Unmarshal(body, &connectedInterfaces)
	if err != nil {
		fmt.Println(err)
	}
	for _, connectedInterface := range connectedInterfaces.Results {
		connectedInterfaceMapping[connectedInterface.Id] = connectedInterface
	}
	cfn.ConnectedInterfaces = connectedInterfaceMapping
}

func (cfn *ConfigFromNetbox) QueryInterfaces(deviceNames []string) {
	// Query Netbox for interfaces on a list of devices. This forms the basis for the
	// rest of the data processing
	quotedDeviceNames := []string{}
	for _, deviceName := range deviceNames {
		quotedDeviceNames = append(quotedDeviceNames, strconv.Quote(deviceName))
	}
	devicesString := strings.Join(quotedDeviceNames, ",")
	queryMap := map[string]string{}
	queryMap["query"] = "{device_list(name: [" + devicesString + `])
			{
			id
			name
			platform {
				slug
			}
			site {
			  slug
			}
			interfaces {
			tags { name }
			vrf { name }
			member_interfaces { name id }
			id
			name
			type
			mgmt_only
			enabled
			description
			label
			mtu
			mac_address
			lag {
				name
			}
			speed
			duplex
			mode
			parent {
				name
			}
			tagged_vlans {
				vid
			}
			untagged_vlan {
				vid
				name
				tags { name }
			}
			ip_addresses {
				id
				address
				description
			}
			cable {
				id
			}
			}
		}
		}
	`
	queryBytes, _ := json.Marshal(queryMap)
	resp := cfn.Api.Request("GET", "graphql/", queryBytes)
	body, _ := io.ReadAll(resp.Body)
	responseData := InterfaceQueryResponse{}
	err := json.Unmarshal(body, &responseData)
	if err != nil {
		fmt.Println(err)
	}
	cfn.InterfaceMapping = make(map[string]InterfaceMapping)
	for _, device := range responseData.Data.DeviceList {
		cfn.InterfaceMapping[device.Name] = InterfaceMapping{
			Device:              device,
			Interfaces:          make(map[string]InterfaceQueryInterface),
			SubinterfaceMapping: make(map[string][]string),
		}
		for _, iface := range device.Interfaces {
			cfn.InterfaceMapping[device.Name].Interfaces[iface.Name] = iface
			if iface.Parent.Name != "" {
				cfn.InterfaceMapping[device.Name].SubinterfaceMapping[iface.Parent.Name] = append(cfn.InterfaceMapping[device.Name].SubinterfaceMapping[iface.Parent.Name], iface.Name)
			}
		}
	}
}

type PrefixResponse struct {
	Results []struct {
		Prefix      string `json:"prefix"`
		Description string `json:"description"`
	} `json:"results"`
}

// var allUsageTags = []string{"PEERING", "TRANSIT", "SERVERS", "IQC"}

func (cfn *ConfigFromNetbox) BuildDescriptionMapping() map[string]map[string]string {
	// Monstrosity of a function that builds a mapping of device/interface to
	// description string from various inputs from Netbox
	if len(cfn.ConnectedInterfaces) == 0 {
		panic("Nothing in ConnectedInterfaces, have you run GetConnectedInterfaces?")
	}
	allCategoryTags := strings.Split(os.Getenv("ANANKE_INTERFACE_CATEGORY_TAGS"), ",")
	descriptionMapping := make(map[string]map[string]string)
	for hostname, deviceDetails := range cfn.InterfaceMapping {
		descriptionMapping[hostname] = make(map[string]string)
		for _, iface := range deviceDetails.Interfaces {
			descriptionFields := []string{}
			categoryTags := []string{}
			for _, tag := range iface.Tags {
				if slices.Contains(allCategoryTags, tag.Name) {
					categoryTags = append(categoryTags, tag.Name)
				}
			}
			if iface.UntaggedVlan.Vid != 0 {
				for _, tag := range iface.UntaggedVlan.Tags {
					if slices.Contains(allCategoryTags, tag.Name) {
						categoryTags = append(categoryTags, tag.Name)
					}
				}
			}
			if len(categoryTags) > 0 {
				categoryTagsString := strings.Join(categoryTags, ",")
				descriptionFields = append(descriptionFields, "["+categoryTagsString+"]")
			}
			if iface.UntaggedVlan.Name != "" && iface.Mode == "ACCESS" {
				descriptionFields = append(descriptionFields, iface.UntaggedVlan.Name)
			} else if len(iface.IpAddresses) > 0 {
				address := iface.IpAddresses[len(iface.IpAddresses)-1].Address
				parentPrefixesResponse := cfn.Api.Request("GET", "api/ipam/prefixes/?contains="+address, []byte(address))
				parentPrefixesBody, _ := io.ReadAll(parentPrefixesResponse.Body)
				var parentPrefixes PrefixResponse
				err := json.Unmarshal(parentPrefixesBody, &parentPrefixes)
				if err != nil {
					fmt.Println(err)
				}
				if len(parentPrefixes.Results) > 0 {
					parentPrefix := parentPrefixes.Results[0]
					if parentPrefix.Description != "" {
						descriptionFields = append(descriptionFields, parentPrefix.Description)
					}
				}
			}
			var linkDescriptions []string
			if iface.Type == "LAG" {
				var lagNeighbors []string
				for _, memberInterface := range iface.MemberInterfaces {
					intId, err := strconv.Atoi(memberInterface.Id)
					if err != nil {
						fmt.Println(err)
					}
					if connectedEndpoints, ok := cfn.ConnectedInterfaces[intId]; ok {
						for _, connectedEndpoint := range connectedEndpoints.ConnectedEndpoints {
							lagNeighbors = append(lagNeighbors, connectedEndpoint.Device.Name)
						}
					}
				}
				if len(lagNeighbors) > 0 {
					sort.Strings(lagNeighbors)
					// lagNeighbors = unique(lagNeighbors)
					linkDescriptions = append(linkDescriptions, strings.Join(lagNeighbors, "/"))
				}
			}
			intId, err := strconv.Atoi(iface.Id)
			if err != nil {
				fmt.Println(err)
			}
			connectedInterface, ok := cfn.ConnectedInterfaces[intId]
			if ok {
				if len(connectedInterface.ConnectedEndpoints) == 0 && connectedInterface.Cable.Id != 0 {
					linkDescriptions = append(linkDescriptions, fmt.Sprintf("CABLE %d", connectedInterface.Cable.Id))
				} else {
					for _, connectedEndpoint := range connectedInterface.ConnectedEndpoints {
						if iface.Cable.Id != "" {
							linkDescriptions = append(linkDescriptions, fmt.Sprintf("%s:%s", connectedEndpoint.Device.Name, connectedEndpoint.Name))
							linkDescriptions = append(linkDescriptions, fmt.Sprintf("CABLE %d", connectedEndpoint.Cable))
						} // else if iface.Type == "LAG" && connectedInterface.Lag != nil && strconv.Itoa(iface.ID) == strconv.Itoa(connectedInterface.Lag.ID) {
						// Additional logic for LAG type interfaces if needed
						//}
					}
				}
			}
			if iface.Description != "" {
				descriptionFields = append(descriptionFields, iface.Description)
			}
			descriptionFields = append(descriptionFields, linkDescriptions...)
			if iface.Lag.Name != "" {
				descriptionFields = append(descriptionFields, iface.Lag.Name)
			}
			descriptionMapping[hostname][iface.Name] = strings.Join(descriptionFields, " - ")
		}
	}
	return descriptionMapping
}

type VlanResponse struct {
	Data struct {
		VlanList []VlanEntry `json:"vlan_list"`
	} `json:"data"`
}
type VlanEntry struct {
	Id   string `json:"id"`
	Site struct {
		Slug string `json:"slug"`
	} `json:"site"`
	Name string `json:"name"`
}

func (cfn *ConfigFromNetbox) BuildVlanMapping() map[string][]VlanEntry {
	// Get site to VLAN list mapping
	queryMap := map[string]string{}
	queryMap["query"] = `
		{
		vlan_list(created:"1") {
			id
			site {
			slug
			}
			name
		}
		}
	`
	queryBytes, _ := json.Marshal(queryMap)
	resp := cfn.Api.Request("GET", "graphql/", queryBytes)
	body, _ := io.ReadAll(resp.Body)
	responseData := VlanResponse{}
	err := json.Unmarshal(body, &responseData)
	if err != nil {
		fmt.Println(err)
	}
	vlanMapping := map[string][]VlanEntry{}
	for _, entry := range responseData.Data.VlanList {
		if entry.Site.Slug != "" {
			vlanMapping[entry.Site.Slug] = append(vlanMapping[entry.Site.Slug], entry)
		}
	}
	return vlanMapping
}

type CfnObjects struct {
	Interfaces map[string]repoconfig.RepoConfig
	Lacp       repoconfig.RepoConfig
	Acl        repoconfig.RepoConfig
	Vlans      repoconfig.RepoConfig
	Ospf       repoconfig.RepoConfig
}

func (cfn *ConfigFromNetbox) GetInterfaceDependentBindings(configTypes []string) {
	// Combined function to get all bindings for things that are dependent on looping
	// over the main device/interface list. This includes ACLs, LACP, FHRP, and OSPF for
	// now
	configObjectTranslate := map[string]repoconfig.RepoConfig{
		"ACL":  {Binding: &ocacl.OpenconfigAcl_Acl_Interfaces{}},
		"LACP": {Binding: &oclacp.Lacp{}},
		"OSPF": {Binding: &ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Protocols_Protocol_Ospfv2{}},
	}
	if len(configTypes) == 0 {
		configTypes = []string{"ACL", "LACP", "OSPF"}
	}
	for hostname, deviceDetails := range cfn.InterfaceMapping {
		for _, configType := range configTypes {
			if configType == "ACL" {
				if deviceConfigObjects, ok := cfn.DeviceObjects[hostname]; ok {
					deviceConfigObjects.Acl = configObjectTranslate[configType]
					cfn.DeviceObjects[hostname] = deviceConfigObjects
				}
			} else if configType == "LACP" {
				if deviceConfigObjects, ok := cfn.DeviceObjects[hostname]; ok {
					deviceConfigObjects.Lacp = configObjectTranslate[configType]
					cfn.DeviceObjects[hostname] = deviceConfigObjects
				}
			} else if configType == "OSPF" {
				if deviceConfigObjects, ok := cfn.DeviceObjects[hostname]; ok {
					deviceConfigObjects.Ospf = configObjectTranslate[configType]
					deviceConfigObjects.Ospf.Binding.(*ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Protocols_Protocol_Ospfv2).Areas = &ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Protocols_Protocol_Ospfv2_Areas{}
					area, err := deviceConfigObjects.Ospf.Binding.(*ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Protocols_Protocol_Ospfv2).Areas.NewArea(ocnetinst.UnionUint32(0))
					if err != nil {
						fmt.Println(err)
					}
					area.Config = &ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Protocols_Protocol_Ospfv2_Areas_Area_Config{
						Identifier: ocnetinst.UnionUint32(0),
					}
					area.Interfaces = &ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Protocols_Protocol_Ospfv2_Areas_Area_Interfaces{}
					cfn.DeviceObjects[hostname] = deviceConfigObjects
				}
			}
		}
		for _, iface := range deviceDetails.Interfaces {
			if slices.Contains(configTypes, "ACL") {
				cfn.DeviceObjects[hostname] = GetAclBinding(cfn.DeviceObjects[hostname], hostname, iface)
			}
			if slices.Contains(configTypes, "LACP") {
				cfn.DeviceObjects[hostname] = GetLacpBinding(cfn.DeviceObjects[hostname], hostname, iface)
			}
			if slices.Contains(configTypes, "OSPF") {
				cfn.DeviceObjects[hostname] = GetOspfBinding(cfn.DeviceObjects[hostname], hostname, iface)
			}
		}
	}
}

func GetLacpBinding(cfnObjects CfnObjects, hostname string, iface InterfaceQueryInterface) CfnObjects {
	if iface.Lag.Name != "" {
		_, ok := cfnObjects.Lacp.Binding.(*oclacp.Lacp).Interface[iface.Lag.Name]
		if !ok {
			_, err := cfnObjects.Lacp.Binding.(*oclacp.Lacp).NewInterface(iface.Lag.Name)
			if err != nil {
				fmt.Println(err)
			}
		}
	}
	return cfnObjects
}

func GetOspfBinding(cfnObjects CfnObjects, hostname string, iface InterfaceQueryInterface) CfnObjects {
	ifTags := []string{}
	for _, tag := range iface.Tags {
		ifTags = append(ifTags, tag.Name)
	}
	if slices.Contains(ifTags, "OSPF_ACTIVE") || slices.Contains(ifTags, "OSPF_PASSIVE") {
		area0 := cfnObjects.Ospf.Binding.(*ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Protocols_Protocol_Ospfv2).Areas.Area[ocnetinst.UnionUint32(0)]
		ospfInt, err := area0.Interfaces.NewInterface(iface.Name)
		if err != nil {
			fmt.Println(err)
		}
		ospfInt.Config = &ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Protocols_Protocol_Ospfv2_Areas_Area_Interfaces_Interface_Config{
			Id: ygot.String(iface.Name),
		}
		if slices.Contains(ifTags, "OSPF_PASSIVE") {
			ospfInt.Config.Passive = ygot.Bool(true)
		}
	}
	return cfnObjects
}

func GetAclBinding(cfnObjects CfnObjects, hostname string, iface InterfaceQueryInterface) CfnObjects {
	ifTags := []string{}
	for _, tag := range iface.Tags {
		ifTags = append(ifTags, tag.Name)
	}
	if slices.Contains(ifTags, "TRANSIT") || slices.Contains(ifTags, "PEERING") {
		aclInt, err := cfnObjects.Acl.Binding.(*ocacl.OpenconfigAcl_Acl_Interfaces).NewInterface(iface.Name)
		if err != nil {
			fmt.Println(err)
		}
		aclInt.IngressAclSets = &ocacl.OpenconfigAcl_Acl_Interfaces_Interface_IngressAclSets{}
		aclInt.IngressAclSets.NewIngressAclSet("INGRESS_ACL", ocacl.OpenconfigAcl_ACL_TYPE_ACL_IPV4)
		aclInt.EgressAclSets = &ocacl.OpenconfigAcl_Acl_Interfaces_Interface_EgressAclSets{}
		aclInt.EgressAclSets.NewEgressAclSet("EGRESS_ACL", ocacl.OpenconfigAcl_ACL_TYPE_ACL_IPV4)
		aclInt.InterfaceRef = &ocacl.OpenconfigAcl_Acl_Interfaces_Interface_InterfaceRef{
			Config: &ocacl.OpenconfigAcl_Acl_Interfaces_Interface_InterfaceRef_Config{},
		}
		if iface.Parent.Name == "" {
			aclInt.InterfaceRef.Config.Interface = ygot.String(iface.Name)
		} else {
			idParts := strings.Split(iface.Name, ".")
			subintId, err := strconv.Atoi(idParts[1])
			if err != nil {
				fmt.Println(err)
			}
			aclInt.InterfaceRef.Config.Interface = ygot.String(idParts[0])
			aclInt.InterfaceRef.Config.Subinterface = ygot.Uint32(uint32(subintId))
		}
	}
	return cfnObjects
}

func (cfn ConfigFromNetbox) GetVlanBindings() {
	// Build OpenConfig VLAN bindings from Netbox data
	// Also need to use the uncompressed binding here to get at the main list
	for hostname, deviceDetails := range cfn.InterfaceMapping {
		vlanMapping := cfn.BuildVlanMapping()
		vlans := &ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Vlans{}
		for _, vlan := range vlanMapping[deviceDetails.Device.Site.Slug] {
			vlanInt, err := strconv.Atoi(vlan.Id)
			if err != nil {
				fmt.Println(err)
			}
			vlanEntry, err := vlans.NewVlan(uint16(vlanInt))
			if err != nil {
				fmt.Println(err)
			}

			vlanEntry.Config = &ocnetinst.OpenconfigNetworkInstance_NetworkInstances_NetworkInstance_Vlans_Vlan_Config{
				VlanId: ygot.Uint16(uint16(vlanInt)),
			}
			vlanName := strings.Replace(
				strings.Replace(vlan.Name, " ", "_", -1), "-", "_", -1)
			vlanEntry.Config.Name = ygot.String(strings.ToUpper(vlanName))
		}
		if deviceConfigObjects, ok := cfn.DeviceObjects[hostname]; ok {
			deviceConfigObjects.Vlans = repoconfig.RepoConfig{
				Binding: vlans,
			}
			cfn.DeviceObjects[hostname] = deviceConfigObjects
		}
	}
}

func (cfn *ConfigFromNetbox) GetInterfaceBindings(filterStrings []string, autoDesc bool) {
	// Build OpenConfig bindings from Netbox data. Optional filterStrings arg can be
	// given, which matches on either an interface name or a tag name.
	// We need to use an uncompressed binding here in order to be able to get at the
	// main interface list for insertion into the gNMI path wrapper if using the
	// TOGETHER layout. This adds a bit more verbosity to the code but and we miss out
	// on some of the nice features of the compressed binding.
	// TODO: Fix tag matching to include tags for subinterfaces somehow too. Currently
	// only gets the tags from the parent.
	var descriptionMapping map[string]map[string]string
	if autoDesc {
		descriptionMapping = cfn.BuildDescriptionMapping()
	}
	fhrpMapping := cfn.BuildFhrpMapping()
	targetInterfaces := map[string][]string{}
	// In order to properly target parent interfaces of subinterfaces specified we need
	// to build a mapping
	for hostname, deviceDetails := range cfn.InterfaceMapping {
		for _, iface := range deviceDetails.Interfaces {
			ifaceName := iface.Name
			ifTags := []string{}
			for _, tag := range iface.Tags {
				ifTags = append(ifTags, tag.Name)
			}
			if len(filterStrings) > 0 {
				var skip bool
				skip = true
				if slices.Contains(filterStrings, iface.Name) {
					skip = false
				}
				for _, tag := range ifTags {
					for _, filterObject := range filterStrings {
						if tag == filterObject {
							skip = false
						}
					}
				}
				if skip {
					continue
				}
			}
			if iface.Parent.Name != "" {
				ifaceName = iface.Parent.Name
			}
			targetInterfaces[hostname] = append(targetInterfaces[hostname], ifaceName)
		}
	}
	// and then iterate over the mapping of target interfaces
	for hostname, targetInterfaces := range targetInterfaces {
		interfaceMap := make(map[string]repoconfig.RepoConfig)
		for _, ifaceName := range targetInterfaces {
			deviceDetails := cfn.InterfaceMapping[hostname]
			iface := deviceDetails.Interfaces[ifaceName]
			intBinding := &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface{
				Name: ygot.String(iface.Name),
			}
			ygot.BuildEmptyTree(intBinding)
			intBinding.Config.Name = ygot.String(iface.Name)
			intBinding.Config.Enabled = ygot.Bool(iface.Enabled)
			if autoDesc && descriptionMapping[hostname][iface.Name] != "" {
				intBinding.Config.Description = ygot.String(descriptionMapping[hostname][iface.Name])
			} else if iface.Description != "" {
				intBinding.Config.Description = ygot.String(iface.Description)
			}
			if iface.Mtu != 0 {
				intBinding.Config.Mtu = ygot.Uint16(uint16(iface.Mtu))
			}
			if iface.MacAddress != "" {
				intBinding.Ethernet.Config.MacAddress = ygot.String(iface.MacAddress)
			}
			if iface.Lag.Name != "" {
				intBinding.Ethernet.Config.AggregateId = ygot.String(iface.Lag.Name)
			}
			if iface.Type == "LAG" {
				intBinding.Aggregation.Config.LagType = ocinterfaces.OpenconfigIfAggregate_AggregationType_LACP
				intBinding.Aggregation.Config.MinLinks = ygot.Uint16(1)
			}
			if iface.Speed != 0 {
				switch iface.Speed {
				case 1000000:
					intBinding.Ethernet.Config.PortSpeed = ocinterfaces.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_1GB
				case 10000000:
					intBinding.Ethernet.Config.PortSpeed = ocinterfaces.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_10GB
				case 40000000:
					intBinding.Ethernet.Config.PortSpeed = ocinterfaces.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_40GB
				case 100000000:
					intBinding.Ethernet.Config.PortSpeed = ocinterfaces.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_100GB
				case 400000000:
					intBinding.Ethernet.Config.PortSpeed = ocinterfaces.OpenconfigIfEthernet_ETHERNET_SPEED_SPEED_400GB
				default:
					panic(fmt.Sprintf("Speed %v not supported yet", string(iface.Speed)))
				}
				intBinding.Ethernet.Config.AutoNegotiate = ygot.Bool(false)
			}
			if strings.Contains(iface.Name, "vlan") {
				intBinding.Config.Type = ocinterfaces.IETFInterfaces_InterfaceType_l2vlan
				intBinding.RoutedVlan = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_RoutedVlan{
					Config: &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_RoutedVlan_Config{
						Vlan: &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_RoutedVlan_Config_Vlan_Union_Uint16{
							Uint16: uint16(iface.UntaggedVlan.Vid),
						},
					},
				}
			} else {
				if iface.Mode == "ACCESS" {
					intBinding.Ethernet.SwitchedVlan.Config.InterfaceMode = ocinterfaces.OpenconfigVlanTypes_VlanModeType_ACCESS
					if iface.UntaggedVlan.Vid != 0 {
						intBinding.Ethernet.SwitchedVlan.Config.AccessVlan = ygot.Uint16(
							uint16(iface.UntaggedVlan.Vid),
						)
					}
				}
			}
			if iface.Mode == "TAGGED" {
				intBinding.Ethernet.SwitchedVlan.Config.InterfaceMode = ocinterfaces.OpenconfigVlanTypes_VlanModeType_TRUNK
				if iface.TaggedVlans != nil {
					for _, vlan := range iface.TaggedVlans {
						vlanObj := &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Ethernet_SwitchedVlan_Config_TrunkVlans_Union_Uint16{Uint16: uint16(vlan.Vid)}
						intBinding.Ethernet.SwitchedVlan.Config.TrunkVlans = append(intBinding.Ethernet.SwitchedVlan.Config.TrunkVlans, vlanObj)
					}
				}
				if iface.UntaggedVlan.Vid != 0 {
					intBinding.Ethernet.SwitchedVlan.Config.NativeVlan = ygot.Uint16(uint16(iface.UntaggedVlan.Vid))
				}
			}
			if len(iface.IpAddresses) > 0 {
				if strings.Contains(iface.Name, "vlan") {
					intBinding.RoutedVlan.Ipv4 = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_RoutedVlan_Ipv4{}
					for _, addr := range iface.IpAddresses {
						addrParts := strings.Split(addr.Address, "/")
						prefixLen, err := strconv.ParseUint(addrParts[1], 10, 8)
						if err != nil {
							fmt.Println(err)
						}
						addrCont, err := intBinding.RoutedVlan.Ipv4.Addresses.NewAddress(addrParts[0])
						if err != nil {
							fmt.Println(err)
						}
						addrCont.Config = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_RoutedVlan_Ipv4_Addresses_Address_Config{
							Ip:           ygot.String(addrParts[0]),
							PrefixLength: ygot.Uint8(uint8(prefixLen)),
						}
						if val, ok := fhrpMapping[hostname][iface.Name]; ok {
							for _, fhrpAssignment := range val {
								vrrp, err := addrCont.Vrrp.NewVrrpGroup(uint8(fhrpAssignment.GroupId))
								if err != nil {
									fmt.Println(err)
								}
								vrrp.Config = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_RoutedVlan_Ipv4_Addresses_Address_Vrrp_VrrpGroup_Config{
									VirtualRouterId: ygot.Uint8(uint8(fhrpAssignment.GroupId)),
									VirtualAddress:  []string{fhrpAssignment.VirtualIp},
									Priority:        ygot.Uint8(uint8(fhrpAssignment.Priority)),
								}
							}
						}
					}
				} else {
					subint := &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface{
						Index: ygot.Uint32(0),
						Config: &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Config{
							Index: ygot.Uint32(0),
						},
					}
					intBinding.Subinterfaces = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces{
						Subinterface: map[uint32]*ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface{},
					}
					intBinding.Subinterfaces.Subinterface[0] = subint
					ygot.BuildEmptyTree(subint)
					for _, addr := range iface.IpAddresses {
						addrParts := strings.Split(addr.Address, "/")
						prefixLen, err := strconv.ParseUint(addrParts[1], 10, 8)
						if err != nil {
							fmt.Println(err)
						}
						addrCont, err := subint.Ipv4.Addresses.NewAddress(addrParts[0])
						if err != nil {
							fmt.Println(err)
						}
						addrCont.Config = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Ipv4_Addresses_Address_Config{
							Ip:           ygot.String(addrParts[0]),
							PrefixLength: ygot.Uint8(uint8(prefixLen)),
						}
						if val, ok := fhrpMapping[hostname][iface.Name]; ok {
							for _, fhrpAssignment := range val {
								vrrp, err := addrCont.Vrrp.NewVrrpGroup(uint8(fhrpAssignment.GroupId))
								if err != nil {
									fmt.Println(err)
								}
								vrrp.Config = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Ipv4_Addresses_Address_Vrrp_VrrpGroup_Config{
									VirtualRouterId: ygot.Uint8(uint8(fhrpAssignment.GroupId)),
									VirtualAddress:  []string{fhrpAssignment.VirtualIp},
									Priority:        ygot.Uint8(uint8(fhrpAssignment.Priority)),
								}
							}
						}
					}
				}
			}
			if val, ok := deviceDetails.SubinterfaceMapping[iface.Name]; ok {
				if len(intBinding.Subinterfaces.Subinterface) == 0 {
					intBinding.Subinterfaces = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces{
						Subinterface: map[uint32]*ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface{},
					}
				}
				for _, subintName := range val {
					iface := deviceDetails.Interfaces[subintName]
					re := regexp.MustCompile(`.*\.(\d+)`)
					subintId, err := strconv.Atoi(re.FindAllStringSubmatch(subintName, -1)[0][1])
					if err != nil {
						fmt.Println(err)
					}
					subint := &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface{
						Index: ygot.Uint32(uint32(subintId)),
						Config: &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Config{
							Index: ygot.Uint32(uint32(subintId)),
						},
					}
					intBinding.Subinterfaces.Subinterface[uint32(subintId)] = subint
					if descriptionMapping[hostname][subintName] != "" {
						subint.Config.Description = ygot.String(descriptionMapping[hostname][subintName])
					}
					if iface.Mode == "ACCESS" {
						subint.Vlan = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Vlan{
							Match: &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Vlan_Match{
								SingleTagged: &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Vlan_Match_SingleTagged{
									Config: &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Vlan_Match_SingleTagged_Config{
										VlanId: ygot.Uint16(uint16(iface.UntaggedVlan.Vid)),
									},
								},
							},
						}
					} else {
						panic("Subinterfaces must be access mode")
					}
					if len(iface.IpAddresses) > 0 {
						subint.Ipv4 = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Ipv4{}
						if iface.Mtu != 0 {
							subint.Ipv4.Config.Mtu = ygot.Uint16(uint16(iface.Mtu))
						}
						for _, addr := range iface.IpAddresses {
							addrParts := strings.Split(addr.Address, "/")
							prefixLen, err := strconv.ParseUint(addrParts[1], 10, 8)
							if err != nil {
								fmt.Println(err)
							}
							subint.Ipv4.Addresses = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Ipv4_Addresses{}
							addrCont, err := subint.Ipv4.Addresses.NewAddress(addrParts[0])
							if err != nil {
								fmt.Println(err)
							}
							addrCont.Config = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Ipv4_Addresses_Address_Config{
								Ip:           ygot.String(addrParts[0]),
								PrefixLength: ygot.Uint8(uint8(prefixLen)),
							}
							if val, ok := fhrpMapping[hostname][iface.Name]; ok {
								for _, fhrpAssignment := range val {
									vrrp, err := addrCont.Vrrp.NewVrrpGroup(uint8(fhrpAssignment.GroupId))
									if err != nil {
										fmt.Println(err)
									}
									vrrp.Config = &ocinterfaces.OpenconfigInterfaces_Interfaces_Interface_Subinterfaces_Subinterface_Ipv4_Addresses_Address_Vrrp_VrrpGroup_Config{
										VirtualRouterId: ygot.Uint8(uint8(fhrpAssignment.GroupId)),
										VirtualAddress:  []string{fhrpAssignment.VirtualIp},
										Priority:        ygot.Uint8(uint8(fhrpAssignment.Priority)),
									}
								}
							}
						}
					}
				}
			}
			interfaceMap[iface.Name] = repoconfig.RepoConfig{
				Binding: intBinding,
			}
		}
		if val, ok := cfn.DeviceObjects[hostname]; ok {
			val.Interfaces = interfaceMap
			cfn.DeviceObjects[hostname] = val
		}
	}
}

type FhrpGroupResponse struct {
	Results []struct {
		Id          int    `json:"id"`
		Protocol    string `json:"protocol"`
		GroupId     int    `json:"group_id"`
		AuthKey     string `json:"auth_key"`
		IpAddresses []struct {
			Id      int    `json:"id"`
			Address string `json:"address"`
		} `json:"ip_addresses"`
	}
}

type FhrpAssignmentResponse struct {
	Results []struct {
		Group struct {
			Id      int `json:"id"`
			GroupId int `json:"group_id"`
		} `json:"group"`
		Priority  int `json:"priority"`
		Interface struct {
			Device struct {
				Name string `json:"name"`
			} `json:"device"`
			Name string `json:"name"`
		} `json:"interface"`
	}
}

type FhrpAssignment struct {
	VirtualIp string
	Priority  int
	GroupId   int
}

func (cfn ConfigFromNetbox) BuildFhrpMapping() map[string]map[string][]FhrpAssignment {
	fhrpMapping := map[string]map[string][]FhrpAssignment{}
	resp := cfn.Api.Request("GET", "api/ipam/fhrp-groups/", []byte{})
	body, _ := io.ReadAll(resp.Body)
	fhrpGroupData := FhrpGroupResponse{}
	err := json.Unmarshal(body, &fhrpGroupData)
	if err != nil {
		fmt.Println(err)
	}
	resp = cfn.Api.Request("GET", "api/ipam/fhrp-group-assignments/", []byte{})
	body, _ = io.ReadAll(resp.Body)
	fhrpAssignmentData := FhrpAssignmentResponse{}
	err = json.Unmarshal(body, &fhrpAssignmentData)
	if err != nil {
		fmt.Println(err)
	}
	for _, fhrpGroup := range fhrpGroupData.Results {
		virtualIp := strings.Split(fhrpGroup.IpAddresses[0].Address, "/")[0]
		for _, fhrpAssignment := range fhrpAssignmentData.Results {
			if fhrpAssignment.Group.Id == fhrpGroup.Id {
				if _, ok := fhrpMapping[fhrpAssignment.Interface.Device.Name]; !ok {
					fhrpMapping[fhrpAssignment.Interface.Device.Name] = make(map[string][]FhrpAssignment)
				}
				fhrpAssignmentInst := FhrpAssignment{
					VirtualIp: virtualIp,
					Priority:  fhrpAssignment.Priority,
					GroupId:   fhrpGroup.GroupId,
				}
				if fhrpGroup.Protocol != "vrrp3" {
					// fmt.Printf(
					// 	"Skipping FHRP group ID %v with protocol %v on %v\n",
					// 	fhrpGroup.GroupId,
					// 	fhrpGroup.Protocol,
					// 	fhrpAssignment.Interface.Device.Name)
					continue
				}
				fhrpMapping[fhrpAssignment.Interface.Device.Name][fhrpAssignment.Interface.Name] = append(
					fhrpMapping[fhrpAssignment.Interface.Device.Name][fhrpAssignment.Interface.Name], fhrpAssignmentInst,
				)
			}
		}
	}
	return fhrpMapping
}
